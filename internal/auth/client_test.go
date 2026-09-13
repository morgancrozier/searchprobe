package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

const installedJSON = `{"installed":{"client_id":"123.apps.googleusercontent.com","project_id":"demo","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token","client_secret":"not-a-real-secret","redirect_uris":["http://localhost"]}}`

func TestParseClientJSONInstalled(t *testing.T) {
	cfg, err := ParseClientJSON([]byte(installedJSON))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "123.apps.googleusercontent.com" || cfg.ClientSecret != "not-a-real-secret" || cfg.ProjectID != "demo" {
		t.Errorf("unexpected: %+v", cfg)
	}
	if cfg.TokenURI != "https://oauth2.googleapis.com/token" {
		t.Errorf("token uri: %s", cfg.TokenURI)
	}
}

func TestParseClientJSONDefaultsEndpoints(t *testing.T) {
	cfg, err := ParseClientJSON([]byte(`{"installed":{"client_id":"abc"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthURI != defaultAuthURI || cfg.TokenURI != defaultTokenURI {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestParseClientJSONErrors(t *testing.T) {
	cases := map[string]string{
		"web client":               `{"web":{"client_id":"abc"}}`,
		"empty":                    `{}`,
		"no id":                    `{"installed":{"client_secret":"x"}}`,
		"invalid":                  `{not json`,
		"untrusted token endpoint": `{"installed":{"client_id":"abc","token_uri":"https://example.com/token"}}`,
	}
	for name, in := range cases {
		_, err := ParseClientJSON([]byte(in))
		var ge *gscerr.Error
		if !errors.As(err, &ge) || ge.Code != gscerr.CodeConfigError || ge.Action == "" {
			t.Errorf("%s: want CONFIG_ERROR with action, got %v", name, err)
		}
	}
}

func TestLoadClientFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(p, []byte(installedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClientFile(p); err != nil {
		t.Fatal(err)
	}
	_, err := LoadClientFile(filepath.Join(t.TempDir(), "missing.json"))
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeConfigError {
		t.Errorf("missing file: %v", err)
	}
}
