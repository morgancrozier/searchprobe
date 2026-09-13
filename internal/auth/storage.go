package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/morgancrozier/searchprobe/internal/config"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// Vault owns OS credential operations. Implementations never put secrets in argv.
type Vault interface {
	Get(service, account string) ([]byte, error)
	Set(service, account string, data []byte) error
	Delete(service, account string) error
}

const vaultService = "com.searchprobe.gsc"

type selection struct {
	Backend string `json:"backend"`
	Record  string `json:"record,omitempty"`
}

func storageError() error {
	return gscerr.New(gscerr.CodeConfigError, "Could not access or verify the OS credential store.", "Unlock Keychain/Secret Service and retry, or explicitly select --credential-store file (plaintext protected by file permissions).")
}
func (s *Store) selectionPath() string { return filepath.Join(filepath.Dir(s.Path), "storage.json") }
func (s *Store) readSelection() error {
	b, err := os.ReadFile(s.selectionPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return storageError()
	}
	var v selection
	if json.Unmarshal(b, &v) != nil || (v.Backend != "file" && v.Backend != "keychain") || (v.Backend == "keychain" && v.Record == "") {
		return gscerr.New(gscerr.CodeConfigError, "Credential storage metadata is invalid.", "Restore storage.json from a trusted backup; do not discard existing credentials.")
	}
	s.backend, s.record = v.Backend, v.Record
	return nil
}
func (s *Store) Backend() string {
	if s.backend == "keychain" {
		return "keychain"
	}
	return "file"
}
func (s *Store) CredentialsPath() any {
	if s.Backend() == "keychain" {
		return nil
	}
	return s.Path
}
func (s *Store) LegacyFile() bool {
	_, err := os.Stat(s.Path)
	return s.managed && s.backend == "" && err == nil
}
func (s *Store) readData() ([]byte, error) {
	if s.Backend() == "keychain" {
		b, err := s.vault.Get(vaultService, s.record)
		if err != nil {
			return nil, storageError()
		}
		return b, nil
	}
	return os.ReadFile(s.Path)
}
func (s *Store) newRecord() (string, error) {
	dir, err := filepath.Abs(filepath.Dir(s.Path))
	if err != nil {
		return "", err
	}
	if resolved, e := filepath.EvalSymlinks(dir); e == nil {
		dir = resolved
	}
	h := sha256.Sum256([]byte(dir))
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return hex.EncodeToString(h[:]) + "-" + hex.EncodeToString(nonce), nil
}

// Select only changes the pending destination. Save commits it after readback.
func (s *Store) Select(kind string) (*Store, error) {
	if kind != "auto" && kind != "keychain" && kind != "file" {
		return nil, gscerr.New(gscerr.CodeInvalidArgument, "Unknown credential store.", "Choose auto, keychain, or file.")
	}
	next := *s
	if kind == "auto" {
		if s.backend != "" || !s.managed || s.LegacyFile() {
			return &next, nil
		}
		kind = "keychain"
	}
	next.backend = kind
	if next.vault == nil {
		next.vault = platformVault{}
	}
	if kind == "keychain" {
		id, err := next.newRecord()
		if err != nil {
			return nil, storageError()
		}
		probe := []byte("SearchProbe storage availability check")
		if err := next.vault.Set(vaultService, id, probe); err != nil {
			return nil, storageError()
		}
		b, e := next.vault.Get(vaultService, id)
		d := next.vault.Delete(vaultService, id)
		if e != nil || d != nil || !bytes.Equal(b, probe) {
			return nil, storageError()
		}
	}
	return &next, nil
}
func (s *Store) writeData(data []byte) error {
	if !s.managed {
		return config.WriteSecretFile(s.Path, data)
	}
	// Read the committed selection separately: s may represent a pending migration.
	old := &Store{Path: s.Path, managed: true, vault: s.vault}
	if err := old.readSelection(); err != nil {
		return err
	}
	if s.Backend() == "file" {
		// Keep a prior file record if committing metadata fails.
		prior, readErr := os.ReadFile(s.Path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if err := config.WriteSecretFile(s.Path, data); err != nil {
			return err
		}
		meta, _ := json.Marshal(selection{Backend: "file"})
		if err := config.WriteSecretFile(s.selectionPath(), meta); err != nil {
			if readErr == nil {
				_ = config.WriteSecretFile(s.Path, prior)
			} else {
				_ = os.Remove(s.Path)
			}
			return err
		}
	} else {
		id, err := s.newRecord()
		if err != nil {
			return storageError()
		}
		if err := s.vault.Set(vaultService, id, data); err != nil {
			_ = s.vault.Delete(vaultService, id)
			return storageError()
		}
		check, err := s.vault.Get(vaultService, id)
		if err != nil || !bytes.Equal(check, data) {
			_ = s.vault.Delete(vaultService, id)
			return storageError()
		}
		meta, _ := json.Marshal(selection{Backend: "keychain", Record: id})
		if err := config.WriteSecretFile(s.selectionPath(), meta); err != nil {
			_ = s.vault.Delete(vaultService, id)
			return err
		}
		s.record = id
	}
	// Destination is committed and verified before deleting the previous secret.
	if old.Backend() == "keychain" && old.record != "" && (s.Backend() == "file" || old.record != s.record) {
		if err := s.vault.Delete(vaultService, old.record); err != nil {
			return gscerr.New(gscerr.CodeConfigError, "Credentials saved, but the previous OS record could not be removed.", "Unlock the OS store and remove the old SearchProbe record; the active record is identified in storage.json.")
		}
	}
	if s.Backend() == "keychain" {
		if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return gscerr.New(gscerr.CodeConfigError, "Credentials saved in the OS store, but the old local credentials file could not be removed.", "Remove the old credentials.json after checking gsc auth status.")
		}
	}
	return nil
}
func (s *Store) deleteKeychain() (bool, error) {
	if err := s.vault.Delete(vaultService, s.record); err != nil {
		return false, storageError()
	}
	if err := os.Remove(s.selectionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	return true, nil
}
