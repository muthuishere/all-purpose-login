package store

import "errors"

// Sentinel errors. Callers use errors.Is.
var (
	ErrNotFound            = errors.New("store: record not found")
	ErrKeychainUnavailable = errors.New("store: keychain unavailable")
	ErrCorruptRecord       = errors.New("store: corrupt record")
)
