package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func oauthConfig(c ClientConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       []string{ScopeReadOnly},
		Endpoint: oauth2.Endpoint{
			AuthURL:   c.AuthURI,
			TokenURL:  c.TokenURI,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// persistingSource wraps an oauth2.TokenSource and writes refreshed tokens
// back to the store so access-token refresh is transparent across runs.
type persistingSource struct {
	mu    sync.Mutex
	src   oauth2.TokenSource
	creds *Credentials
	store *Store
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := RequireBYO(p.creds.Client); err != nil {
		return nil, err
	}
	tok, err := p.src.Token()
	if err != nil {
		return nil, MapTokenError(err)
	}
	if tok.AccessToken != p.creds.Token.AccessToken || !tok.Expiry.Equal(p.creds.Token.Expiry) {
		if tok.RefreshToken == "" {
			tok.RefreshToken = p.creds.Token.RefreshToken
		}
		p.creds.setToken(tok)
		if p.store != nil {
			if err := p.store.Save(p.creds); err != nil {
				return nil, err
			}
		}
	}
	return tok, nil
}

// HTTPClient returns an HTTP client that attaches a valid access token to
// every request, refreshing (and persisting) it when expired.
func HTTPClient(ctx context.Context, creds *Credentials, store *Store) *http.Client {
	conf := oauthConfig(creds.Client)
	base := conf.TokenSource(ctx, creds.OAuth2Token())
	src := &persistingSource{src: base, creds: creds, store: store}
	hc := oauth2.NewClient(ctx, src)
	hc.Timeout = 60 * time.Second
	return hc
}

// MapTokenError converts token-endpoint failures into normalized errors.
func MapTokenError(err error) error {
	var ge *gscerr.Error
	if errors.As(err, &ge) {
		return ge
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "invalid_grant":
			return gscerr.Wrap(err, gscerr.CodeAuthRevoked,
				"The stored Google credentials were revoked or have expired.",
				"Run `gsc auth login` to sign in again with the saved client.")
		case "invalid_client", "unauthorized_client":
			return gscerr.Wrap(err, gscerr.CodeAuthFailed,
				"Google rejected the OAuth client used for these credentials.",
				"Check the OAuth client in Google Cloud Console, then run `gsc auth login` again.")
		case "invalid_scope":
			return gscerr.Wrap(err, gscerr.CodeAuthScopeInsufficient,
				"Google rejected the requested Search Console scope.",
				"Ensure the Search Console API is enabled for the OAuth client's project, then run `gsc auth login` again.")
		}
		return gscerr.Wrap(err, gscerr.CodeAuthFailed,
			"Google token request failed.",
			"Run `gsc auth login` to sign in again with the saved client.")
	}
	if isNetworkError(err) {
		return gscerr.Wrap(err, gscerr.CodeNetworkError,
			"Could not reach Google's OAuth token endpoint.",
			"Check your network connection and retry.")
	}
	return gscerr.Wrap(err, gscerr.CodeAuthFailed,
		"Could not obtain a Google access token.",
		"Run `gsc auth login` to sign in again with the saved client.")
}

func isNetworkError(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	var oe *net.OpError
	return errors.As(err, &oe)
}
