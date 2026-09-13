package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirResolution(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	t.Setenv("XDG_CONFIG_HOME", "")

	t.Setenv(EnvConfigDir, "/custom/gsc")
	got, err := Dir()
	if err != nil || got != "/custom/gsc" {
		t.Fatalf("GSC_CONFIG_DIR: got %q, %v", got, err)
	}

	t.Setenv(EnvConfigDir, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err = Dir()
	if err != nil || got != filepath.Join("/xdg", "gsc") {
		t.Fatalf("XDG_CONFIG_HOME: got %q, %v", got, err)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, _ := os.UserHomeDir()
	got, err = Dir()
	if err != nil || got != filepath.Join(home, ".config", "gsc") {
		t.Fatalf("default: got %q, %v", got, err)
	}

	p, err := CredentialsPath()
	if err != nil || filepath.Base(p) != CredentialsFile {
		t.Fatalf("CredentialsPath: got %q, %v", p, err)
	}
}

func TestWriteSecretFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := filepath.Join(t.TempDir(), "nested", "gsc")
	path := filepath.Join(dir, CredentialsFile)

	if err := WriteSecretFile(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	// Overwrite must succeed and replace content.
	if err := WriteSecretFile(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two" {
		t.Fatalf("content: %q, %v", data, err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file perm = %o, want 600", fi.Mode().Perm())
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm = %o, want 700", di.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %d entries", len(entries))
	}
}
