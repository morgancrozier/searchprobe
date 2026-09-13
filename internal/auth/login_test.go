package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func TestAuthURLUsesPKCEAndOfflineAccess(t *testing.T) {
	fl, err := newFlow(ClientConfig{ClientID: "cid", AuthURI: defaultAuthURI, TokenURI: defaultTokenURI}, "http://127.0.0.1:4242/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(fl.authURL())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             "cid",
		"redirect_uri":          "http://127.0.0.1:4242/callback",
		"response_type":         "code",
		"scope":                 ScopeReadOnly,
		"access_type":           "offline",
		"prompt":                "consent",
		"code_challenge_method": "S256",
		"state":                 fl.state,
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge") == fl.verifier {
		t.Errorf("code_challenge must be the S256 hash, not the verifier")
	}
	if len(fl.verifier) < 43 || len(fl.verifier) > 128 {
		t.Errorf("verifier length %d outside PKCE bounds", len(fl.verifier))
	}
	if len(fl.state) < 32 {
		t.Errorf("state too short: %d", len(fl.state))
	}
	if strings.Contains(fl.authURL(), "webmasters ") || strings.Contains(q.Get("scope"), " ") {
		t.Errorf("only the read-only scope may be requested: %q", q.Get("scope"))
	}
}

func TestHandleCallbackRejectsBadState(t *testing.T) {
	fl, _ := newFlow(ClientConfig{ClientID: "cid", AuthURI: defaultAuthURI, TokenURI: defaultTokenURI}, "http://127.0.0.1:1/callback")

	cases := []struct {
		name     string
		query    string
		wantCode string
		wantErr  bool
	}{
		{"wrong state", "code=abc&state=wrong", "", true},
		{"missing state", "code=abc", "", true},
		{"google error", "error=access_denied&state=" + fl.state, "", true},
		{"missing code", "state=" + fl.state, "", true},
		{"ok", "code=abc&state=" + fl.state, "abc", false},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback?"+tc.query, nil)
		code, err := fl.handleCallback(rec, req)
		if (err != nil) != tc.wantErr || code != tc.wantCode {
			t.Errorf("%s: code=%q err=%v", tc.name, code, err)
		}
		if err != nil {
			var ge *gscerr.Error
			if !errors.As(err, &ge) || ge.Code != gscerr.CodeAuthFailed {
				t.Errorf("%s: want AUTH_FAILED, got %v", tc.name, err)
			}
		}
		if strings.Contains(rec.Body.String(), "abc") {
			t.Errorf("%s: callback page must not echo the code", tc.name)
		}
		for _, secret := range []string{fl.state, "access-fixture", "refresh-fixture"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s: callback page exposed sensitive value %q", tc.name, secret)
			}
		}
		if !strings.Contains(rec.Body.String(), "SearchProbe") {
			t.Errorf("%s: callback page is not SearchProbe branded", tc.name)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", tc.name, got)
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Errorf("%s: Cache-Control = %q", tc.name, got)
		}
	}
}

func TestSuccessfulCallbackPageIsBrandedAndTokenSafe(t *testing.T) {
	fl, _ := newFlow(ClientConfig{ClientID: "client-fixture", AuthURI: defaultAuthURI, TokenURI: defaultTokenURI}, "http://127.0.0.1:1/callback")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/callback?code=code-fixture&state="+url.QueryEscape(fl.state)+"&access_token=access-fixture&refresh_token=refresh-fixture", nil)

	code, err := fl.handleCallback(rec, req)
	if err != nil || code != "code-fixture" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	body := rec.Body.String()
	for _, want := range []string{"SearchProbe", "Google Search Console is connected with read-only access.", "return to your terminal"} {
		if !strings.Contains(body, want) {
			t.Errorf("callback page missing %q", want)
		}
	}
	for _, sensitive := range []string{"code-fixture", fl.state, "access-fixture", "refresh-fixture", "client-fixture"} {
		if strings.Contains(body, sensitive) {
			t.Errorf("callback page exposed %q", sensitive)
		}
	}
}

func TestCallbackPageEscapesDynamicCopy(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCallbackPage(rec, http.StatusBadRequest, `<script>title</script>`, `<img src=x onerror=alert(1)>`)
	body := rec.Body.String()
	if strings.Contains(body, "<script>title</script>") || strings.Contains(body, "<img src=x") {
		t.Fatalf("callback page did not escape dynamic copy: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;title&lt;/script&gt;") || !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatalf("escaped callback copy missing: %s", body)
	}
}

func TestPresentAuthorizationURL(t *testing.T) {
	const authURL = "https://accounts.example.test/oauth?state=state-fixture"
	tests := []struct {
		name       string
		open       func(string) error
		wantURL    bool
		wantOpened bool
		wantFailed bool
	}{
		{name: "browser opens", open: func(string) error { return nil }, wantOpened: true},
		{name: "browser fails", open: func(string) error { return errors.New("open failed") }, wantURL: true, wantOpened: true, wantFailed: true},
		{name: "manual", open: nil, wantURL: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			presentAuthorization(&out, tc.open, authURL)
			got := out.String()
			if strings.Contains(got, authURL) != tc.wantURL {
				t.Errorf("URL presence = %v, want %v; output %q", strings.Contains(got, authURL), tc.wantURL, got)
			}
			if strings.Contains(got, "Opening Google sign-in in your browser...") != tc.wantOpened {
				t.Errorf("opening message mismatch: %q", got)
			}
			if strings.Contains(got, "Could not open your browser automatically.") != tc.wantFailed {
				t.Errorf("failure message mismatch: %q", got)
			}
		})
	}
}

// TestLoginEndToEnd simulates the browser and Google's token endpoint.
func TestLoginEndToEnd(t *testing.T) {
	var gotExchange url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotExchange, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-fixture",
			"refresh_token": "refresh-fixture",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         ScopeReadOnly,
		})
	}))
	defer tokenSrv.Close()

	client := ClientConfig{ClientID: "cid", ClientSecret: "csecret", AuthURI: "https://example.invalid/auth", TokenURI: tokenSrv.URL}

	browser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		// Simulate Google redirecting the browser back to the loopback server.
		cb := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			resp, err := http.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	var stderr bytes.Buffer
	creds, err := Login(context.Background(), LoginOptions{Client: client, OpenBrowser: browser, Stderr: &stderr, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token.RefreshToken != "refresh-fixture" || creds.Token.AccessToken != "access-fixture" {
		t.Errorf("token not captured: %+v", creds.Token)
	}
	if !creds.HasReadOnlyScope() {
		t.Errorf("scopes: %v", creds.Scopes)
	}
	if gotExchange.Get("code") != "the-code" || gotExchange.Get("grant_type") != "authorization_code" {
		t.Errorf("exchange params: %v", gotExchange)
	}
	if gotExchange.Get("code_verifier") == "" {
		t.Errorf("PKCE verifier not sent on exchange")
	}
	if !strings.HasPrefix(gotExchange.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("redirect_uri must be loopback: %q", gotExchange.Get("redirect_uri"))
	}
	if strings.Contains(stderr.String(), "https://example.invalid/auth") {
		t.Errorf("successful browser open printed authorization URL: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Waiting for authorization...") {
		t.Errorf("missing waiting message: %q", stderr.String())
	}
}

func TestLoginRejectsMissingScope(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "a", "refresh_token": "r", "token_type": "Bearer", "expires_in": 3600,
			"scope": "https://www.googleapis.com/auth/userinfo.email",
		})
	}))
	defer tokenSrv.Close()
	client := ClientConfig{ClientID: "cid", AuthURI: "https://example.invalid/auth", TokenURI: tokenSrv.URL}
	browser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := Login(context.Background(), LoginOptions{Client: client, OpenBrowser: browser, Timeout: 10 * time.Second})
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeAuthScopeInsufficient {
		t.Fatalf("want AUTH_SCOPE_INSUFFICIENT, got %v", err)
	}
}

func TestLoginTimeout(t *testing.T) {
	client := ClientConfig{ClientID: "cid", AuthURI: "https://example.invalid/auth", TokenURI: "https://example.invalid/token"}
	_, err := Login(context.Background(), LoginOptions{Client: client, Timeout: 100 * time.Millisecond})
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeAuthFailed || !strings.Contains(ge.Message, "Timed out") {
		t.Fatalf("want timeout AUTH_FAILED, got %v", err)
	}
}

func TestPersistingSourceRefreshesAndSaves(t *testing.T) {
	calls := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-fixture" {
			t.Errorf("unexpected refresh request: %v", form)
		}
		for _, k := range []string{"prompt", "access_type", "code_challenge", "code_verifier"} {
			if form.Has(k) {
				t.Errorf("refresh request must not carry %q (login-only parameter)", k)
			}
		}
		if strings.Contains(r.URL.RawQuery, "prompt") {
			t.Errorf("refresh URL must not carry prompt: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-new", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer tokenSrv.Close()

	store := &Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	creds := testCreds()
	creds.Client.TokenURI = tokenSrv.URL
	creds.Token.AccessToken = "access-expired"
	creds.Token.Expiry = time.Now().Add(-time.Hour)
	if err := store.Save(creds); err != nil {
		t.Fatal(err)
	}

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-new" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()

	hc := HTTPClient(context.Background(), creds, store)
	for i := 0; i < 2; i++ {
		resp, err := hc.Get(apiSrv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if calls != 1 {
		t.Errorf("refresh calls = %d, want 1 (token should be reused)", calls)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Token.AccessToken != "access-new" || reloaded.Token.RefreshToken != "refresh-fixture" {
		t.Errorf("refreshed token not persisted: %+v", reloaded.Token)
	}
}

func TestMapTokenError(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&oauth2.RetrieveError{ErrorCode: "invalid_grant"}, gscerr.CodeAuthRevoked},
		{&oauth2.RetrieveError{ErrorCode: "invalid_client"}, gscerr.CodeAuthFailed},
		{&oauth2.RetrieveError{ErrorCode: "invalid_scope"}, gscerr.CodeAuthScopeInsufficient},
		{&oauth2.RetrieveError{ErrorCode: "server_error", ErrorDescription: "x"}, gscerr.CodeAuthFailed},
		{&url.Error{Op: "Post", URL: "https://oauth2.googleapis.com/token", Err: errors.New("dial tcp: no route")}, gscerr.CodeNetworkError},
		{gscerr.New(gscerr.CodeConfigError, "m", "a"), gscerr.CodeConfigError},
	}
	for _, tc := range cases {
		got := gscerr.From(MapTokenError(tc.err))
		if got.Code != tc.code {
			t.Errorf("%v: code %s, want %s", tc.err, got.Code, tc.code)
		}
		if got.Action == "" && tc.code != gscerr.CodeConfigError {
			t.Errorf("%v: action must be set", tc.err)
		}
		if strings.Contains(got.Message, "refresh-fixture") {
			t.Errorf("token leaked into message")
		}
	}
}

func TestRevoke(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// Temporarily point revocation at the test server via a custom transport.
	hc := &http.Client{Transport: rewriteTransport{target: srv.URL}}
	if err := Revoke(context.Background(), hc, "refresh-fixture"); err != nil {
		t.Fatal(err)
	}
	if got != "token=refresh-fixture" {
		t.Errorf("body = %q", got)
	}
	if err := Revoke(context.Background(), hc, ""); err != nil {
		t.Errorf("empty token must be a no-op: %v", err)
	}
}

type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(rt.target)
	r.URL.Scheme = u.Scheme
	r.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestAccessDeniedGuidance(t *testing.T) {
	fl, err := newFlow(fixtureClient(), "http://127.0.0.1:1/callback")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/callback?error=access_denied&state="+fl.state+"&error_description=private-fixture&code=code-fixture", nil)
	code, err := fl.handleCallback(rec, req)
	ge := gscerr.From(err)
	if code != "" || ge.Code != gscerr.CodeAuthFailed || !strings.Contains(ge.Message, "access_denied") {
		t.Fatalf("denial was not reported: code=%q error=%v", code, err)
	}
	for _, want := range []string{"If you cancelled consent", "exact error shown by Google", "if it identifies an organization policy", "--client-file"} {
		if !strings.Contains(ge.Action, want) {
			t.Errorf("guidance missing %q", want)
		}
	}
	rendered := ge.Message + ge.Action + rec.Body.String()
	for _, unwanted := range []string{"Testing", "test user", "private-fixture", "code-fixture", fl.state} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("denial output contains %q", unwanted)
		}
	}
}
