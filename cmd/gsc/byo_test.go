package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/morgancrozier/searchprobe/internal/auth"
)

func TestBYOImportDeleteDownloadAndReauthenticate(t *testing.T) {
	h := newHarness(t, nil)
	p := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(p, []byte(`{"installed":{"client_id":"my-client","client_secret":"private-fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := h.run("auth", "login", "--client-file", p, "--credential-store", "file", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.deps.login = func(ctx context.Context, opts auth.LoginOptions) (*auth.Credentials, error) {
		if opts.Client.ClientID != "my-client" || opts.Client.ClientSecret != "private-fixture" {
			t.Fatal("lost imported client")
		}
		return fakeCreds(opts.Client), nil
	}
	if code := h.run("auth", "login", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	assertNoSecrets(t, h.stdout.String()+h.stderr.String())
	if code := h.run("auth", "logout", "--json"); code != 0 {
		t.Fatal(code)
	}
	if code := h.run("auth", "login", "--json"); code != 3 {
		t.Fatal("logout retained client")
	}
}
func TestLegacyGrantRequiresBYOButCanLogout(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.store.Save(fakeCreds(auth.ClientConfig{ClientID: auth.LegacyClientID})); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"auth", "status", "--json"}, {"auth", "login", "--json"}, {"setup", "--agent", "none", "--json"}} {
		if code := h.run(args...); code != 3 {
			t.Fatal(args, code, h.stdout.String())
		}
	}
	if code := h.run("auth", "logout", "--json"); code != 0 {
		t.Fatal(code)
	}
}
