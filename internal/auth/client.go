// Package auth implements Google Desktop OAuth (PKCE S256 + loopback
// callback) and local credential persistence for the read-only Search
// Console scope.
package auth

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

const (
	// ScopeReadOnly is the only scope gsc ever requests.
	ScopeReadOnly = "https://www.googleapis.com/auth/webmasters.readonly"

	defaultAuthURI  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURI = "https://oauth2.googleapis.com/token"
	// RevokeURI is Google's token revocation endpoint.
	RevokeURI = "https://oauth2.googleapis.com/revoke"
)

// ClientConfig is the subset of a Google OAuth client definition gsc needs.
//
// Google Desktop clients are public clients: the "secret" cannot be kept
// confidential in an installed binary, but Google still expects it on the
// token endpoint for this client type, so it is retained locally.
type ClientConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AuthURI      string `json:"authUri"`
	TokenURI     string `json:"tokenUri"`
	ProjectID    string `json:"projectId,omitempty"`
}

// googleClientFile mirrors the JSON downloaded from Google Cloud Console.
type googleClientFile struct {
	Installed *googleClientEntry `json:"installed"`
	Web       *googleClientEntry `json:"web"`
}

type googleClientEntry struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
	ProjectID    string `json:"project_id"`
}

// LoadClientFile reads a Google OAuth client JSON file. Only "Desktop app"
// clients (the "installed" key) are accepted because the loopback redirect
// flow is specific to that client type.
func LoadClientFile(path string) (ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClientConfig{}, gscerr.Wrap(err, gscerr.CodeConfigError,
			fmt.Sprintf("Could not read OAuth client file %q.", path),
			"Download the Desktop app OAuth client JSON from Google Cloud Console and pass its path with --client-file.")
	}
	return ParseClientJSON(data)
}

// ParseClientJSON parses the contents of a Google OAuth client JSON file.
func ParseClientJSON(data []byte) (ClientConfig, error) {
	var f googleClientFile
	if err := json.Unmarshal(data, &f); err != nil {
		return ClientConfig{}, gscerr.Wrap(err, gscerr.CodeConfigError,
			"The OAuth client file is not valid JSON.",
			"Download the Desktop app OAuth client JSON from Google Cloud Console without editing it.")
	}
	if f.Installed == nil {
		if f.Web != nil {
			return ClientConfig{}, gscerr.New(gscerr.CodeConfigError,
				"The OAuth client file describes a Web application client, but gsc requires a Desktop app client.",
				"In Google Cloud Console create an OAuth client of type \"Desktop app\" and download its JSON.")
		}
		return ClientConfig{}, gscerr.New(gscerr.CodeConfigError,
			"The OAuth client file does not contain an \"installed\" client definition.",
			"Download the Desktop app OAuth client JSON from Google Cloud Console.")
	}
	e := f.Installed
	if e.ClientID == "" {
		return ClientConfig{}, gscerr.New(gscerr.CodeConfigError,
			"The OAuth client file is missing client_id.",
			"Download a fresh Desktop app OAuth client JSON from Google Cloud Console.")
	}
	cfg := ClientConfig{
		ClientID:     e.ClientID,
		ClientSecret: e.ClientSecret,
		AuthURI:      e.AuthURI,
		TokenURI:     e.TokenURI,
		ProjectID:    e.ProjectID,
	}
	if cfg.AuthURI == "" {
		cfg.AuthURI = defaultAuthURI
	}
	if cfg.TokenURI == "" {
		cfg.TokenURI = defaultTokenURI
	}
	if (cfg.AuthURI != defaultAuthURI && cfg.AuthURI != "https://accounts.google.com/o/oauth2/auth") || cfg.TokenURI != defaultTokenURI {
		return ClientConfig{}, gscerr.New(gscerr.CodeConfigError, "The client JSON uses unexpected OAuth endpoints.", "Download an unmodified Google Cloud Desktop OAuth client JSON. SearchProbe sends credentials only to Google.")
	}
	return cfg, nil
}
