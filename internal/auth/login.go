package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// LoginOptions controls the interactive login flow.
type LoginOptions struct {
	Client ClientConfig
	// OpenBrowser launches the system browser at the given URL. If nil, the
	// URL is only printed to Stderr.
	OpenBrowser func(url string) error
	// Stderr receives human-facing progress messages. May be nil.
	Stderr io.Writer
	// Timeout bounds how long to wait for the browser callback.
	Timeout time.Duration
	// ListenAddr is the loopback address to bind. Defaults to 127.0.0.1:0.
	ListenAddr string
}

// Login runs the Desktop OAuth flow: PKCE S256, random state, loopback
// callback on a random port, offline access, then exchanges the code.
func Login(ctx context.Context, opts LoginOptions) (*Credentials, error) {
	if err := RequireBYO(opts.Client); err != nil {
		return nil, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = "127.0.0.1:0"
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Client.ClientID == "" {
		return nil, gscerr.New(gscerr.CodeConfigError, "OAuth client ID is empty.", "Pass a valid Desktop app client JSON with --client-file.")
	}

	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, gscerr.Wrap(err, gscerr.CodeInternal, "Could not open a loopback port for the OAuth callback.", "Check that local TCP listeners are permitted and retry.")
	}
	defer ln.Close()
	if !isLoopback(ln.Addr()) {
		return nil, gscerr.New(gscerr.CodeInternal, "Refusing to bind the OAuth callback to a non-loopback address.", "")
	}
	redirectURL := fmt.Sprintf("http://%s/callback", ln.Addr().String())

	fl, err := newFlow(opts.Client, redirectURL)
	if err != nil {
		return nil, err
	}

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code, cbErr := fl.handleCallback(w, r)
		select {
		case results <- result{code: code, err: cbErr}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := fl.authURL()
	presentAuthorization(opts.Stderr, opts.OpenBrowser, authURL)
	fmt.Fprintln(opts.Stderr)
	fmt.Fprintln(opts.Stderr, "Waiting for authorization...")

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	var code string
	select {
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return nil, gscerr.New(gscerr.CodeAuthFailed, "Timed out waiting for the Google sign-in to complete.", "Run `gsc auth login` again and finish the consent screen in the browser.")
		}
		return nil, gscerr.New(gscerr.CodeAuthFailed, "Login was cancelled.", "")
	}

	tok, err := fl.exchange(ctx, code)
	if err != nil {
		return nil, err
	}

	scopes := grantedScopes(tok)
	if len(scopes) > 0 && !contains(scopes, ScopeReadOnly) {
		return nil, gscerr.New(gscerr.CodeAuthScopeInsufficient,
			"The Google account did not grant the read-only Search Console scope.",
			"Run `gsc auth login` again and allow Search Console access on the consent screen.")
	}
	if len(scopes) == 0 {
		scopes = []string{ScopeReadOnly}
	}
	if tok.RefreshToken == "" {
		return nil, gscerr.New(gscerr.CodeAuthFailed,
			"Google did not return a refresh token, so the login cannot be persisted.",
			"Remove gsc's access at https://myaccount.google.com/permissions and run `gsc auth login` again.")
	}

	creds := &Credentials{Client: opts.Client, Scopes: scopes}
	creds.setToken(tok)
	creds.CreatedAt = creds.UpdatedAt
	return creds, nil
}

func presentAuthorization(w io.Writer, openBrowser func(string) error, authURL string) {
	if openBrowser == nil {
		fmt.Fprintln(w, "Open this URL to continue:")
		fmt.Fprintln(w, authURL)
		return
	}

	fmt.Fprintln(w, "Opening Google sign-in in your browser...")
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Could not open your browser automatically.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Open this URL to continue:")
		fmt.Fprintln(w, authURL)
	}
}

// flow holds the per-login PKCE verifier and state.
type flow struct {
	conf     *oauth2.Config
	state    string
	verifier string
}

func newFlow(client ClientConfig, redirectURL string) (*flow, error) {
	state, err := randomToken(32)
	if err != nil {
		return nil, gscerr.Wrap(err, gscerr.CodeInternal, "Could not generate OAuth state.", "")
	}
	conf := oauthConfig(client)
	conf.RedirectURL = redirectURL
	return &flow{
		conf:     conf,
		state:    state,
		verifier: oauth2.GenerateVerifier(),
	}, nil
}

func (f *flow) authURL() string {
	return f.conf.AuthCodeURL(f.state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(f.verifier),
		// prompt=consent guarantees a refresh token is issued even when the
		// account has previously authorized this client. It applies only to
		// this interactive authorization URL; token refresh goes straight to
		// the token endpoint and never carries it.
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

func (f *flow) exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	tok, err := f.conf.Exchange(ctx, code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return nil, MapTokenError(err)
	}
	return tok, nil
}

// handleCallback validates the redirect and responds to the browser. It
// returns the authorization code or a normalized error.
func (f *flow) handleCallback(w http.ResponseWriter, r *http.Request) (string, error) {
	q := r.URL.Query()
	if got := q.Get("state"); got == "" || got != f.state {
		writeCallbackPage(w, http.StatusBadRequest, "Sign-in failed", "SearchProbe could not verify this sign-in. Close this tab and try again from your terminal.")
		return "", gscerr.New(gscerr.CodeAuthFailed, "OAuth state mismatch on callback; the response was rejected.", "Run `gsc auth login` again.")
	}
	if e := q.Get("error"); e != "" {
		writeCallbackPage(w, http.StatusOK, "Sign-in not completed", "SearchProbe was not connected. You can close this tab and return to your terminal.")
		msg := "Google sign-in did not complete."

		action := "Run `gsc auth login` again."
		if e == "access_denied" {
			msg = "Google authorization was denied (access_denied)."
			action = "If you cancelled consent, run `gsc auth login` again and allow read-only Search Console access. Otherwise, check the exact error shown by Google in the browser; contact your administrator if it identifies an organization policy, or report the error to SearchProbe. For BYO credentials, repeat --client-file."
		}
		return "", gscerr.New(gscerr.CodeAuthFailed, msg, action)
	}
	code := q.Get("code")
	if code == "" {
		writeCallbackPage(w, http.StatusBadRequest, "Sign-in failed", "SearchProbe did not receive the authorization it needed. Close this tab and try again from your terminal.")
		return "", gscerr.New(gscerr.CodeAuthFailed, "Google's callback did not include an authorization code.", "Run `gsc auth login` again.")
	}
	writeCallbackPage(w, http.StatusOK, "You're signed in to SearchProbe", "Google Search Console is connected with read-only access. You can close this tab and return to your terminal.")
	return code, nil
}

func writeCallbackPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    :root { color-scheme: light dark; }
    * { box-sizing: border-box; }
    body {
      min-height: 100vh;
      margin: 0;
      display: grid;
      place-items: center;
      padding: 2rem;
      background: #f4f3ee;
      color: #17211b;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    main {
      width: min(100%%, 34rem);
      padding: 2.5rem;
      border: 1px solid #d9ddd7;
      border-radius: 1.25rem;
      background: #fff;
      box-shadow: 0 1.25rem 3.5rem rgba(23, 33, 27, .09);
    }
    .brand {
      margin: 0 0 2.25rem;
      color: #36724c;
      font-size: .78rem;
      font-weight: 750;
      letter-spacing: .12em;
      text-transform: uppercase;
    }
    h1 { margin: 0; font-size: clamp(1.8rem, 5vw, 2.35rem); line-height: 1.12; letter-spacing: -.035em; }
    p { margin: 1rem 0 0; color: #526158; font-size: 1rem; line-height: 1.65; }
    @media (prefers-color-scheme: dark) {
      body { background: #101612; color: #edf3ee; }
      main { background: #18211b; border-color: #2e3b32; box-shadow: 0 1.25rem 3.5rem rgba(0, 0, 0, .28); }
      .brand { color: #84c999; }
      p { color: #b7c5ba; }
    }
  </style>
</head>
<body>
  <main>
    <div class="brand">SearchProbe</div>
    <h1>%s</h1>
    <p>%s</p>
  </main>
</body>
</html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}

// grantedScopes reads the space-delimited scope list Google returns with a token.
func grantedScopes(tok *oauth2.Token) []string {
	raw, _ := tok.Extra("scope").(string)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Fields(raw)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcp.IP.IsLoopback()
}

// Revoke asks Google to revoke a token. It is best-effort: callers should
// remove local credentials even if revocation fails.
func Revoke(ctx context.Context, hc *http.Client, token string) error {
	if token == "" {
		return nil
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, RevokeURI, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
