package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/morgancrozier/searchprobe/internal/config"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// credentialsVersion is bumped if the on-disk format changes incompatibly.
const credentialsVersion = 1

// StoredToken is the persisted OAuth token. Never print it.
type StoredToken struct {
	AccessToken  string    `json:"accessToken"`
	TokenType    string    `json:"tokenType"`
	RefreshToken string    `json:"refreshToken"`
	Expiry       time.Time `json:"expiry"`
}

// Credentials is the on-disk credential record.
type Credentials struct {
	Version   int          `json:"version"`
	Client    ClientConfig `json:"client"`
	Scopes    []string     `json:"scopes"`
	Token     StoredToken  `json:"token"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// OAuth2Token converts the stored token to the oauth2 library type.
func (c *Credentials) OAuth2Token() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  c.Token.AccessToken,
		TokenType:    c.Token.TokenType,
		RefreshToken: c.Token.RefreshToken,
		Expiry:       c.Token.Expiry,
	}
}

func (c *Credentials) setToken(t *oauth2.Token) {
	c.Token = StoredToken{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.Expiry,
	}
	c.UpdatedAt = time.Now().UTC()
}

// HasReadOnlyScope reports whether the stored grant includes the scope gsc needs.
func (c *Credentials) HasReadOnlyScope() bool {
	for _, s := range c.Scopes {
		if s == ScopeReadOnly {
			return true
		}
	}
	return false
}

// Store persists credentials to a single permissions-restricted file.
type Store struct {
	Path    string
	backend string
	record  string
	vault   Vault
	managed bool
}

// DefaultStore returns the store at the configured credentials path.
func DefaultStore() (*Store, error) {
	p, err := config.CredentialsPath()
	if err != nil {
		return nil, gscerr.Wrap(err, gscerr.CodeConfigError, "Could not resolve the gsc config directory.", "Set GSC_CONFIG_DIR to a writable directory.")
	}
	s := &Store{Path: p, managed: true, vault: platformVault{}}
	if err := s.readSelection(); err != nil {
		return nil, err
	}
	return s, nil
}

// Load reads stored credentials. It returns AUTH_REQUIRED when none exist.
func (s *Store) Load() (*Credentials, error) {
	data, err := s.readData()
	if err != nil {
		var ge *gscerr.Error
		if errors.As(err, &ge) {
			return nil, err
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, gscerr.AuthRequired()
		}
		return nil, gscerr.Wrap(err, gscerr.CodeConfigError,
			fmt.Sprintf("Could not read credentials file %q.", s.Path),
			"Check the file permissions or run `gsc auth login` again.")
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, gscerr.Wrap(err, gscerr.CodeConfigError,
			fmt.Sprintf("Credentials file %q is corrupt.", s.Path),
			"Run `gsc auth logout` and then `gsc auth login`.")
	}
	if c.Version != credentialsVersion || c.Client.ClientID == "" || c.Token.RefreshToken == "" {
		return nil, gscerr.New(gscerr.CodeAuthRequired,
			"Stored credentials are incomplete or from an incompatible version.",
			"Run `gsc auth login`.")
	}
	return &c, nil
}

// Save writes credentials with 0600 permissions.
func (s *Store) Save(c *Credentials) error {
	c.Version = credentialsVersion
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return gscerr.Wrap(err, gscerr.CodeInternal, "Could not encode credentials.", "")
	}
	if err := s.writeData(data); err != nil {
		var ge *gscerr.Error
		if errors.As(err, &ge) {
			return err
		}
		return gscerr.Wrap(err, gscerr.CodeConfigError,
			fmt.Sprintf("Could not write credentials file %q.", s.Path),
			"Check that the config directory is writable, or set GSC_CONFIG_DIR.")
	}
	return nil
}

// Delete removes stored credentials. Missing credentials are not an error.
func (s *Store) Delete() (removed bool, err error) {
	if s.Backend() == "keychain" {
		return s.deleteKeychain()
	}
	if err := os.Remove(s.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, gscerr.Wrap(err, gscerr.CodeConfigError,
			fmt.Sprintf("Could not remove credentials file %q.", s.Path), "")
	}
	if s.managed {
		_ = os.Remove(s.selectionPath())
	}
	return true, nil
}
