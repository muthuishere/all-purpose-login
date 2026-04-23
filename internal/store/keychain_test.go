package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

func newKeychainStoreForTest(t *testing.T) Store {
	t.Helper()
	// Fresh mock keychain + isolated temp path per test.
	keyring.MockInit()
	dir := t.TempDir()
	return newKeychainStoreAt(filepath.Join(dir, "cred.json"))
}

func TestKeychainStore_Contract(t *testing.T) {
	storeContract(t, newKeychainStoreForTest)
}

func TestKeychainStore_EncryptsOnDisk(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	path := filepath.Join(dir, "cred.json")
	s := newKeychainStoreAt(path)

	rec := &TokenRecord{
		Provider:     "google",
		Label:        "work",
		RefreshToken: "my-secret-refresh-token",
		AccessToken:  "my-secret-access-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cred: %v", err)
	}
	if contains(raw, "my-secret-refresh-token") || contains(raw, "my-secret-access-token") {
		t.Fatal("plaintext token leaked into cred.json on disk")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cred: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cred.json perms = %o, want 0600", info.Mode().Perm())
	}
}

func TestKeychainStore_MasterKeyStoredInKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := newKeychainStoreAt(filepath.Join(dir, "cred.json"))
	rec := &TokenRecord{Provider: "google", Label: "work", RefreshToken: "rt"}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Master key must now exist in the mock keychain under our service name.
	got, err := keyring.Get(keychainService, masterKeyName)
	if err != nil {
		t.Fatalf("master key missing from keychain: %v", err)
	}
	if got == "" {
		t.Fatal("master key empty")
	}
}

func TestKeychainStore_GetMissingErrNotFound(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := newKeychainStoreAt(filepath.Join(dir, "cred.json"))
	_, err := s.Get(context.Background(), "google:nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

func contains(b []byte, needle string) bool {
	n := []byte(needle)
	if len(n) == 0 || len(b) < len(n) {
		return false
	}
	for i := 0; i+len(n) <= len(b); i++ {
		match := true
		for j := 0; j < len(n); j++ {
			if b[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
