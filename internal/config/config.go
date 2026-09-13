// Package config resolves where gsc stores its local state.
//
// Resolution order for the config directory:
//  1. $GSC_CONFIG_DIR
//  2. $XDG_CONFIG_HOME/gsc
//  3. ~/.config/gsc
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// EnvConfigDir overrides the config directory.
	EnvConfigDir = "GSC_CONFIG_DIR"
	// CredentialsFile holds the OAuth client and token material (mode 0600).
	CredentialsFile = "credentials.json"

	dirPerm  = 0o700
	filePerm = 0o600
)

// Dir returns the gsc configuration directory without creating it.
func Dir() (string, error) {
	if v := os.Getenv(EnvConfigDir); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "gsc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "gsc"), nil
}

// CredentialsPath returns the path of the credentials file.
func CredentialsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CredentialsFile), nil
}

// WriteSecretFile atomically writes data to path with 0600 permissions,
// creating the parent directory with 0700 if needed.
func WriteSecretFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	// Tighten an existing directory's permissions if it was created loosely.
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("protect config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(filePerm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("set file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close file: %w", err)
	}
	check, err := os.ReadFile(tmpName)
	if err != nil || !bytes.Equal(check, data) {
		cleanup()
		return fmt.Errorf("verify secret file failed")
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
