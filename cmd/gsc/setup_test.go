package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morgancrozier/searchprobe/internal/agent"
	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

func setupHarness(t *testing.T, response string) (*harness, string) {
	t.Helper()
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) })
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.deps.agentEnvironment = func() (agent.Environment, error) { return agent.Environment{Home: home}, nil }
	return h, home
}

const setupOneSite = `{"siteEntry":[{"siteUrl":"sc-domain:example.com","permissionLevel":"siteOwner"}]}`

func TestSetupFreshAndRepeat(t *testing.T) {
	h, home := setupHarness(t, setupOneSite)
	h.deps.interactive = func() bool { return true }
	clientFile := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(clientFile, []byte(`{"installed":{"client_id":"cid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	h.deps.stdin = strings.NewReader("\n" + clientFile + "\n") // both agents followed by client path
	logins := 0
	h.deps.login = func(ctx context.Context, opts auth.LoginOptions) (*auth.Credentials, error) {
		logins++
		if opts.Client != fakeCreds(auth.ClientConfig{}).Client {
			t.Fatal("not using imported OAuth")
		}
		return fakeCreds(opts.Client), nil
	}
	if code := h.run("setup"); code != 0 {
		t.Fatal(code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Try this first:") || !strings.Contains(h.stdout.String(), "sc-domain:example.com") {
		t.Fatal(h.stdout.String())
	}
	paths := []string{filepath.Join(home, ".claude/skills/searchprobe/SKILL.md"), filepath.Join(home, ".agents/skills/searchprobe/SKILL.md")}
	before := make([]os.FileInfo, len(paths))
	for i, p := range paths {
		var err error
		before[i], err = os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
	}
	if code := h.run("setup", "--agent", "all", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String(), h.stderr.String())
	}
	if logins != 1 || len(h.paths) != 2 {
		t.Fatal("did not reuse auth and recheck access", logins, h.paths)
	}
	for i, p := range paths {
		after, err := os.Stat(p)
		if err != nil || !after.ModTime().Equal(before[i].ModTime()) {
			t.Fatal("rewrote unchanged skill", p, err)
		}
	}
	var result struct {
		Data struct {
			Ready       bool
			FirstPrompt string
		}
		Meta struct{ AgentActivationVerified bool }
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Data.Ready || result.Meta.AgentActivationVerified {
		t.Fatal(h.stdout.String())
	}
	assertNoSecrets(t, h.stdout.String()+h.stderr.String())
}

func TestSetupPreflightAndNoBrowserInPipes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		interactive bool
		input       string
		code        int
	}{
		{"pipe", []string{"setup"}, false, "", 2},
		{"json needs selection", []string{"setup", "--json"}, true, "", 2},
		{"json never logs in", []string{"setup", "--agent", "all", "--json"}, true, "", 3},
		{"pipe never logs in", []string{"setup", "--agent", "all"}, false, "", 3},
		{"invalid", []string{"setup", "--agent", "typo"}, true, "", 2},
		{"EOF", []string{"setup"}, true, "", 2},
		{"unknown answer", []string{"setup"}, true, "typo\n", 2},
		{"auto none detected", []string{"setup", "--agent", "auto"}, true, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, home := setupHarness(t, setupOneSite)
			h.deps.interactive = func() bool { return tc.interactive }
			h.deps.stdin = strings.NewReader(tc.input)
			h.deps.login = func(context.Context, auth.LoginOptions) (*auth.Credentials, error) {
				t.Fatal("unexpected login")
				return nil, nil
			}
			if code := h.run(tc.args...); code != tc.code {
				t.Fatal(code, h.stdout.String(), h.stderr.String())
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Fatal("wrote agent directories", entries)
			}
			if _, err := os.Stat(h.store.Path); !os.IsNotExist(err) {
				t.Fatal("wrote credentials", err)
			}
		})
	}
}

func TestSetupExistingAuthStates(t *testing.T) {
	for _, code := range []string{gscerr.CodeAuthRevoked, gscerr.CodeNetworkError, gscerr.CodePropertyAccessDenied} {
		t.Run(code, func(t *testing.T) {
			h, home := setupHarness(t, setupOneSite)
			h.signIn(t)
			h.deps.interactive = func() bool { return true }
			oldClient := h.deps.client
			calls, logins := 0, 0
			h.deps.client = func(ctx context.Context) (*searchconsole.Client, error) {
				calls++
				if calls == 1 {
					return nil, gscerr.New(code, "fixture failure", "retry")
				}
				return oldClient(ctx)
			}
			h.deps.login = func(ctx context.Context, opts auth.LoginOptions) (*auth.Credentials, error) {
				logins++
				if opts.Client.ClientID != "cid" {
					t.Fatal("lost BYO client")
				}
				return fakeCreds(opts.Client), nil
			}
			got := h.run("setup", "--agent", "all")
			if code == gscerr.CodeAuthRevoked {
				if got != 0 || logins != 1 {
					t.Fatal(got, logins, h.stderr.String())
				}
			} else {
				if got == 0 || logins != 0 {
					t.Fatal(got, logins)
				}
				entries, _ := os.ReadDir(home)
				if len(entries) != 0 {
					t.Fatal("installed despite network/access failure")
				}
			}
		})
	}
}

func TestSetupConflictsBeforeAuth(t *testing.T) {
	h, home := setupHarness(t, setupOneSite)
	custom := filepath.Join(home, ".claude/skills/searchprobe")
	if err := os.MkdirAll(custom, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(custom, "SKILL.md")
	os.WriteFile(file, []byte("my skill"), 0600)
	if code := h.run("setup", "--agent", "all", "--json"); code != 1 {
		t.Fatal(code, h.stdout.String())
	}
	content, _ := os.ReadFile(file)
	if string(content) != "my skill" {
		t.Fatal("changed user skill")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("partial install during preflight")
	}
}

func TestSetupNoPropertiesAndTerminalOnly(t *testing.T) {
	for _, response := range []string{`{}`, `{"siteEntry":[{"siteUrl":"sc-domain:example.com","permissionLevel":"siteUnverifiedUser"}]}`, setupOneSite} {
		h, home := setupHarness(t, response)
		h.signIn(t)
		if code := h.run("setup", "--agent", "none", "--json"); code != 0 {
			t.Fatal(code, h.stdout.String())
		}
		var result struct {
			Data struct {
				Ready       bool
				FirstPrompt string
				NextStep    string
			}
		}
		if err := json.Unmarshal(h.stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.Ready != (response == setupOneSite) || result.Data.FirstPrompt != "" {
			t.Fatal(h.stdout.String())
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 0 {
			t.Fatal("terminal-only created agent directories")
		}
	}
}

func TestSetupDetectedOnlyAndMultipleProperties(t *testing.T) {
	h, home := setupHarness(t, `{"siteEntry":[{"siteUrl":"sc-domain:a.com","permissionLevel":"siteOwner"},{"siteUrl":"sc-domain:b.com","permissionLevel":"siteFullUser"}]}`)
	h.signIn(t)
	os.Mkdir(filepath.Join(home, ".claude"), 0700)
	if code := h.run("setup", "--agent", "auto", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	if !strings.Contains(h.stdout.String(), "ask which matches this repository") {
		t.Fatal("guessed a property", h.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("configured undetected agent")
	}
}

func TestSetupUpdatesPreviouslyPreparedUndetectedSkill(t *testing.T) {
	h, home := setupHarness(t, setupOneSite)
	h.signIn(t)
	if code := h.run("agent", "install", "--agent", "codex"); code != 0 {
		t.Fatal(code)
	}
	dir := filepath.Join(home, ".agents/skills/searchprobe")
	old := "old managed skill"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".searchprobe-sha256"), []byte(fmt.Sprintf("%x\n", sha256.Sum256([]byte(old)))), 0644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("setup", "--agent", "auto", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	contents, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if string(contents) == old {
		t.Fatal("did not update undetected managed skill")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("installed undetected missing agent")
	}
}

func TestSetupPartialFailureCanResume(t *testing.T) {
	h, home := setupHarness(t, setupOneSite)
	h.signIn(t)
	lock := filepath.Join(home, ".agents/skills/searchprobe.lock")
	os.MkdirAll(filepath.Dir(lock), 0755)
	os.WriteFile(lock, []byte(""), 0600)
	if code := h.run("setup", "--agent", "all", "--json"); code != 1 {
		t.Fatal(code, h.stdout.String())
	}
	if !strings.Contains(h.stdout.String(), "Earlier completed steps are retained") {
		t.Fatal(h.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/searchprobe/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	os.Remove(lock)
	if code := h.run("setup", "--agent", "all", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
}

func TestSetupFailedReauthRetainsCredentials(t *testing.T) {
	h, home := setupHarness(t, setupOneSite)
	h.signIn(t)
	h.deps.interactive = func() bool { return true }
	before, _ := os.ReadFile(h.store.Path)
	h.deps.client = func(context.Context) (*searchconsole.Client, error) {
		return nil, gscerr.New(gscerr.CodeAuthRevoked, "revoked", "")
	}
	h.deps.login = func(context.Context, auth.LoginOptions) (*auth.Credentials, error) {
		return nil, gscerr.New(gscerr.CodeAuthFailed, "cancelled", "")
	}
	if code := h.run("setup", "--agent", "all"); code == 0 {
		t.Fatal("claimed success")
	}
	after, _ := os.ReadFile(h.store.Path)
	if string(after) != string(before) {
		t.Fatal("replaced credentials")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("installed skills after failed auth")
	}
}

func TestSetupRefreshesExpiredTokenWithoutLogin(t *testing.T) {
	h, _ := setupHarness(t, setupOneSite)
	refreshed, listed := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			refreshed++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" {
				t.Error("not a refresh")
			}
			fmt.Fprint(w, `{"access_token":"refreshed-fixture","token_type":"Bearer","expires_in":3600}`)
			return
		}
		listed++
		if r.Header.Get("Authorization") != "Bearer refreshed-fixture" {
			t.Error("did not use refreshed token")
		}
		fmt.Fprint(w, setupOneSite)
	}))
	defer server.Close()
	creds := fakeCreds(fakeCreds(auth.ClientConfig{}).Client)
	creds.Client.TokenURI = server.URL + "/token"
	creds.Token.Expiry = time.Now().Add(-time.Hour)
	if err := h.store.Save(creds); err != nil {
		t.Fatal(err)
	}
	h.deps.client = func(ctx context.Context) (*searchconsole.Client, error) {
		c, err := h.store.Load()
		if err != nil {
			return nil, err
		}
		client := searchconsole.New(auth.HTTPClient(ctx, c, h.store))
		client.BaseURL = server.URL
		return client, nil
	}
	h.deps.login = func(context.Context, auth.LoginOptions) (*auth.Credentials, error) {
		t.Fatal("unexpected browser login")
		return nil, nil
	}
	if code := h.run("setup", "--agent", "none", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	saved, err := h.store.Load()
	if err != nil || saved.Token.AccessToken != "refreshed-fixture" || refreshed != 1 || listed != 1 {
		t.Fatal("refresh not persisted", err, refreshed, listed)
	}
	if strings.Contains(h.stdout.String()+h.stderr.String(), "refreshed-fixture") {
		t.Fatal("token leaked")
	}
}

func TestSetupSelectionCancellation(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := setupAnswer(ctx, input); err != context.Canceled {
		t.Fatal(err)
	}
}
