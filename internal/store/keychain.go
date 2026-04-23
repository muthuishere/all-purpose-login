package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/zalando/go-keyring"
)

// Architecture:
//   - macOS keychain (via go-keyring) holds ONE 32-byte AES-256 master key
//     under service="all-purpose-login", account="master-key".
//   - All token records live in an AES-256-GCM encrypted file at
//     $XDG_CONFIG_HOME/apl/cred.json (file 0600, dir 0700).
//   - The keychain's per-entry size limit (~4KB on macOS) no longer constrains
//     us: records can be arbitrarily large. One keychain prompt per process
//     unlocks all accounts.
//
// File format (plaintext wrapper):
//
//	{"version":1,"nonce":"<base64>","ct":"<base64>"}
//
// Decrypting ct yields JSON: {"records": {"provider:label": TokenRecord, ...}}.

const (
	keychainService = "all-purpose-login"
	masterKeyName   = "master-key"
	credVersion     = 1
	keySize         = 32 // AES-256
)

type keychainStore struct {
	mu   sync.Mutex
	path string // cred.json path
	key  []byte // cached AES-256 key
}

func newKeychainStore() (*keychainStore, error) {
	p, err := DefaultCredPath()
	if err != nil {
		return nil, err
	}
	return &keychainStore{path: p}, nil
}

// newKeychainStoreAt is used by tests to inject a cred.json path.
func newKeychainStoreAt(path string) *keychainStore {
	return &keychainStore{path: path}
}

// DefaultCredPath is $XDG_CONFIG_HOME/apl/cred.json or $HOME/.config/apl/cred.json.
func DefaultCredPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "apl", "cred.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "apl", "cred.json"), nil
}

type credFile struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"` // base64
	CT      string `json:"ct"`    // base64
}

type credPlain struct {
	Records map[string]*TokenRecord `json:"records"`
}

func (s *keychainStore) Put(ctx context.Context, rec *TokenRecord) error {
	rec.Handle = rec.HandleString()
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := s.loadPlainLocked()
	if err != nil {
		return err
	}
	if plain.Records == nil {
		plain.Records = map[string]*TokenRecord{}
	}
	plain.Records[rec.Handle] = rec
	return s.savePlainLocked(plain)
}

func (s *keychainStore) Get(ctx context.Context, handle string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := s.loadPlainLocked()
	if err != nil {
		return nil, err
	}
	rec, ok := plain.Records[handle]
	if !ok {
		return nil, ErrNotFound
	}
	return rec, nil
}

func (s *keychainStore) List(ctx context.Context) ([]*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := s.loadPlainLocked()
	if err != nil {
		return nil, err
	}
	out := make([]*TokenRecord, 0, len(plain.Records))
	for _, r := range plain.Records {
		out = append(out, r)
	}
	return out, nil
}

func (s *keychainStore) Delete(ctx context.Context, handle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := s.loadPlainLocked()
	if err != nil {
		return err
	}
	if _, ok := plain.Records[handle]; !ok {
		return ErrNotFound
	}
	delete(plain.Records, handle)
	return s.savePlainLocked(plain)
}

// --- encryption + file plumbing ---------------------------------------------

func (s *keychainStore) getKey() ([]byte, error) {
	if s.key != nil {
		return s.key, nil
	}
	raw, err := keyring.Get(keychainService, masterKeyName)
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(raw)
		if derr != nil || len(k) != keySize {
			return nil, fmt.Errorf("%w: master key corrupt", ErrCorruptRecord)
		}
		s.key = k
		return k, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, mapKeychainErr(err)
	}
	// First use — generate and store.
	k := make([]byte, keySize)
	if _, rerr := rand.Read(k); rerr != nil {
		return nil, fmt.Errorf("store: generate master key: %w", rerr)
	}
	if serr := keyring.Set(keychainService, masterKeyName, base64.StdEncoding.EncodeToString(k)); serr != nil {
		return nil, mapKeychainErr(serr)
	}
	s.key = k
	return k, nil
}

func (s *keychainStore) loadPlainLocked() (*credPlain, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &credPlain{Records: map[string]*TokenRecord{}}, nil
		}
		return nil, fmt.Errorf("store: read %s: %w", s.path, err)
	}
	var wrap credFile
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("%w: wrapper parse: %v", ErrCorruptRecord, err)
	}
	if wrap.Version != credVersion {
		return nil, fmt.Errorf("%w: unsupported cred version %d", ErrCorruptRecord, wrap.Version)
	}
	nonce, err := base64.StdEncoding.DecodeString(wrap.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: nonce decode: %v", ErrCorruptRecord, err)
	}
	ct, err := base64.StdEncoding.DecodeString(wrap.CT)
	if err != nil {
		return nil, fmt.Errorf("%w: ct decode: %v", ErrCorruptRecord, err)
	}
	key, err := s.getKey()
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt: %v", ErrCorruptRecord, err)
	}
	var plain credPlain
	if err := json.Unmarshal(pt, &plain); err != nil {
		return nil, fmt.Errorf("%w: inner parse: %v", ErrCorruptRecord, err)
	}
	if plain.Records == nil {
		plain.Records = map[string]*TokenRecord{}
	}
	return &plain, nil
}

func (s *keychainStore) savePlainLocked(plain *credPlain) error {
	key, err := s.getKey()
	if err != nil {
		return err
	}
	aead, err := newGCM(key)
	if err != nil {
		return err
	}
	pt, err := json.Marshal(plain)
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("store: nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, pt, nil)
	wrap := credFile{
		Version: credVersion,
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		CT:      base64.StdEncoding.EncodeToString(ct),
	}
	body, err := json.MarshalIndent(wrap, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal wrapper: %w", err)
	}
	// Atomic write: temp + rename, with 0600 on file, 0700 on dir.
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store: mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("store: chmod %s: %w", dir, err)
	}
	tmp := s.path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("store: create temp: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("store: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: aes init: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: gcm init: %w", err)
	}
	return aead, nil
}

func mapKeychainErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return fmt.Errorf("%w: %s", ErrKeychainUnavailable, keychainUnavailableMessage)
	}
	return fmt.Errorf("store: %w", err)
}

const keychainUnavailableMessage = "keychain unavailable on this system (libsecret not found / Secret Service not running). Install/start the OS secret service."
