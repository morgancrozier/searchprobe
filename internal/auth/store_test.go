package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func testCreds() *Credentials {
	return &Credentials{
		Client: ClientConfig{ClientID: "id", ClientSecret: "secret", AuthURI: defaultAuthURI, TokenURI: defaultTokenURI},
		Scopes: []string{ScopeReadOnly},
		Token: StoredToken{
			AccessToken:  "access-fixture",
			TokenType:    "Bearer",
			RefreshToken: "refresh-fixture",
			Expiry:       time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		},
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "gsc", "credentials.json")}

	_, err := s.Load()
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeAuthRequired {
		t.Fatalf("Load before save: want AUTH_REQUIRED, got %v", err)
	}

	in := testCreds()
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(s.Path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("perm = %o", fi.Mode().Perm())
		}
	}
	out, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.Token.RefreshToken != "refresh-fixture" || out.Client.ClientID != "id" || !out.HasReadOnlyScope() {
		t.Errorf("round trip mismatch: %+v", out)
	}
	if out.Version != credentialsVersion || out.CreatedAt.IsZero() {
		t.Errorf("metadata not set: %+v", out)
	}

	removed, err := s.Delete()
	if err != nil || !removed {
		t.Fatalf("Delete: %v %v", removed, err)
	}
	removed, err = s.Delete()
	if err != nil || removed {
		t.Fatalf("second Delete: %v %v", removed, err)
	}
}

func TestStoreLoadCorrupt(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := os.WriteFile(s.Path, []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeConfigError || !strings.Contains(ge.Action, "logout") {
		t.Errorf("corrupt: %v", err)
	}
}

func TestStoreLoadIncomplete(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := os.WriteFile(s.Path, []byte(`{"version":1,"client":{"clientId":"id"},"token":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodeAuthRequired {
		t.Errorf("incomplete: %v", err)
	}
}
