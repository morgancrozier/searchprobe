package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryVault struct {
	records                   map[string][]byte
	failSet, failGet, corrupt bool
}

func (v *memoryVault) Set(service, id string, b []byte) error {
	if v.failSet {
		return errors.New("secret-fixture access denied")
	}
	v.records[id] = append([]byte(nil), b...)
	return nil
}
func (v *memoryVault) Get(service, id string) ([]byte, error) {
	if v.failGet {
		return nil, errors.New("secret-fixture locked")
	}
	b, ok := v.records[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	if v.corrupt {
		return []byte("corrupt"), nil
	}
	return b, nil
}
func (v *memoryVault) Delete(service, id string) error { delete(v.records, id); return nil }
func managedTestStore(t *testing.T) (*Store, *memoryVault) {
	t.Helper()
	v := &memoryVault{records: map[string][]byte{}}
	return &Store{Path: filepath.Join(t.TempDir(), "credentials.json"), managed: true, vault: v}, v
}
func reopenTestStore(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{Path: s.Path, managed: true, vault: s.vault}
	if err := n.readSelection(); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestManagedStorageMigrationAndReadback(t *testing.T) {
	s, v := managedTestStore(t)
	c := testCreds()
	// Existing credentials are readable before storage metadata exists.
	if err := (&Store{Path: s.Path}).Save(c); err != nil {
		t.Fatal(err)
	}
	if !s.LegacyFile() {
		t.Fatal("legacy file not detected")
	}
	n, err := s.Select("keychain")
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Save(c); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatal("plaintext survived migration")
	}
	b, _ := os.ReadFile(s.selectionPath())
	if bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("refresh-fixture")) {
		t.Fatal("secret in metadata")
	}
	n = reopenTestStore(t, n)
	out, err := n.Load()
	if err != nil || out.Client != c.Client {
		t.Fatal("client not retained")
	}
	if n.CredentialsPath() != nil || n.Backend() != "keychain" {
		t.Fatal("wrong status")
	}
	// Refresh commits a new verified record then removes the previous record.
	out.Token.AccessToken = "refreshed"
	if err = n.Save(out); err != nil {
		t.Fatal(err)
	}
	if len(v.records) != 1 {
		t.Fatal("stale records")
	}
	f, err := n.Select("file")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Save(out); err != nil {
		t.Fatal(err)
	}
	if len(v.records) != 0 {
		t.Fatal("OS record survived file migration")
	}
	f = reopenTestStore(t, f)
	if f.Backend() != "file" {
		t.Fatal("lost explicit file selection")
	}
	if removed, err := f.Delete(); err != nil || !removed {
		t.Fatal("logout failed")
	}
}
func TestStorageFailureRetainsCommittedCredentials(t *testing.T) {
	for _, mode := range []string{"write", "read", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			s, v := managedTestStore(t)
			s.backend = "keychain"
			if err := s.Save(testCreds()); err != nil {
				t.Fatal(err)
			}
			prior, _ := os.ReadFile(s.selectionPath())
			oldRecord := s.record
			switch mode {
			case "write":
				v.failSet = true
			case "read":
				v.failGet = true
			case "corrupt":
				v.corrupt = true
			}
			c := testCreds()
			c.Client.ClientID = "replacement"
			err := s.Save(c)
			if err == nil {
				t.Fatal("false success")
			}
			if strings.Contains(err.Error(), "secret-fixture") {
				t.Fatal("secret leaked")
			}
			after, _ := os.ReadFile(s.selectionPath())
			if !bytes.Equal(prior, after) || v.records[oldRecord] == nil {
				t.Fatal("committed credentials lost")
			}
			if len(v.records) != 1 {
				t.Fatal("failed pending record leaked")
			}
		})
	}
}
func TestUnavailableStoreRequiresExplicitFile(t *testing.T) {
	s, v := managedTestStore(t)
	v.failSet = true
	if _, err := s.Select("auto"); err == nil {
		t.Fatal("silent downgrade")
	}
	f, err := s.Select("file")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Save(testCreds()); err != nil {
		t.Fatal(err)
	}
	n, err := reopenTestStore(t, f).Select("auto")
	if err != nil || n.Backend() != "file" {
		t.Fatal("did not reuse choice")
	}
}
func TestRecordNamespaces(t *testing.T) {
	a, _ := managedTestStore(t)
	b, _ := managedTestStore(t)
	x, _ := a.newRecord()
	y, _ := b.newRecord()
	if x[:64] == y[:64] {
		t.Fatal("config namespaces collide")
	}
}
func TestKeychainIntegration(t *testing.T) {
	if os.Getenv("GSC_TEST_KEYCHAIN") != "1" {
		t.Skip("set GSC_TEST_KEYCHAIN=1 for native OS storage")
	}
	s := &Store{Path: filepath.Join(t.TempDir(), "credentials.json"), managed: true, vault: platformVault{}}
	// This diagnostic stores only a public fixture, never real credentials.
	probeID, err := s.newRecord()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.vault.Set(vaultService, probeID, []byte("public integration probe")); err != nil {
		t.Fatalf("native fixture probe failed: %v", err)
	}
	if err := s.vault.Delete(vaultService, probeID); err != nil {
		t.Fatalf("native fixture cleanup failed: %v", err)
	}
	n, err := s.Select("keychain")
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Save(testCreds()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := n.Delete(); e != nil {
			t.Error(e)
		}
	})
	loaded := reopenTestStore(t, n)
	c, err := loaded.Load()
	if err != nil || c.Client.ClientID != "id" {
		t.Fatal("native readback failed")
	}
	c.Token.AccessToken = "updated-fixture"
	if err = n.Save(c); err != nil {
		t.Fatal(err)
	}
}
