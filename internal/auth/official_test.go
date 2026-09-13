package auth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopFlow(t *testing.T) {
	c := fixtureClient()
	if c.ClientID == "" || c.ClientSecret == "" || c.ProjectID != "searchprobe" || c.AuthURI != defaultAuthURI || c.TokenURI != defaultTokenURI {
		t.Fatal("invalid official configuration")
	}
	f, err := newFlow(c, "http://127.0.0.1:1234/callback")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newFlow(c, "http://127.0.0.1:1234/callback")
	u, _ := url.Parse(f.authURL())
	q := u.Query()
	if q.Get("scope") != ScopeReadOnly || q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || f.state == other.state || f.verifier == other.verifier {
		t.Fatal("OAuth protections missing")
	}
	if strings.Contains(f.authURL(), c.ClientSecret) {
		t.Fatal("credential in authorization URL")
	}
}

func TestRefreshRetainsSelectedIdentity(t *testing.T) {
	store := &Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	for _, client := range []ClientConfig{fixtureClient(), {ClientID: "byo-id", ClientSecret: "byo-secret", AuthURI: defaultAuthURI}, fixtureClient()} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				calls++
				r.ParseForm()
				if r.Form.Get("client_id") != client.ClientID || r.Form.Get("client_secret") != client.ClientSecret || r.Form.Get("refresh_token") != "refresh-fixture" {
					t.Error("wrong persisted client identity")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token":"new-fixture","token_type":"Bearer","expires_in":3600}`))
				return
			}
			if r.Header.Get("Authorization") != "Bearer new-fixture" {
				t.Error("missing refreshed access")
			}
		}))
		client.TokenURI = srv.URL + "/token"
		c := testCreds()
		c.Client = client
		c.Token.Expiry = time.Now().Add(-time.Hour)
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
		// Fresh store load and HTTP client model a later process invocation.
		loaded, err := (&Store{Path: store.Path}).Load()
		if err != nil {
			t.Fatal(err)
		}
		resp, err := HTTPClient(context.Background(), loaded, store).Get(srv.URL + "/api")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || persisted.Client != client || persisted.Token.AccessToken != "new-fixture" {
			t.Fatal("refresh persistence mismatch")
		}
		srv.Close()
	}
	if removed, err := store.Delete(); err != nil || !removed {
		t.Fatal("logout deletion failed")
	}
}

func TestTokenErrorsDoNotEchoTokens(t *testing.T) {
	for _, err := range []error{&oauth2.RetrieveError{ErrorCode: "server_error", ErrorDescription: "access-fixture refresh-fixture", Body: []byte("access-fixture refresh-fixture")}, errors.New("access-fixture refresh-fixture")} {
		mapped := MapTokenError(err)
		b, _ := json.Marshal(gscerr.From(mapped))
		for _, out := range []string{mapped.Error(), string(b)} {
			if strings.Contains(out, "access-fixture") || strings.Contains(out, "refresh-fixture") {
				t.Fatal("token in error")
			}
		}
	}
}

func TestCallbackErrorsDoNotEchoTokens(t *testing.T) {
	f, _ := newFlow(fixtureClient(), "http://127.0.0.1:1234/callback")
	r := httptest.NewRequest("GET", "/callback?state="+f.state+"&error=access-fixture&error_description=refresh-fixture", nil)
	w := httptest.NewRecorder()
	_, err := f.handleCallback(w, r)
	if err == nil {
		t.Fatal("expected callback failure")
	}
	b, _ := json.Marshal(gscerr.From(err))
	for _, out := range []string{err.Error(), string(b), w.Body.String()} {
		if strings.Contains(out, "access-fixture") || strings.Contains(out, "refresh-fixture") {
			t.Fatal("token in callback output")
		}
	}
}

func fixtureClient() ClientConfig {
	return ClientConfig{ClientID: "fixture-client", ClientSecret: "fixture-secret", ProjectID: "searchprobe", AuthURI: defaultAuthURI, TokenURI: defaultTokenURI}
}
