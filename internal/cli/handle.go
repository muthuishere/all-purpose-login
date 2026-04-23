package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/provider"
)

// Handle is a parsed "provider:label" pair.
type Handle struct {
	Provider string
	Label    string
}

func (h Handle) String() string { return h.Provider + ":" + h.Label }

var handleRe = regexp.MustCompile(`^[a-z]+:[a-zA-Z0-9._-]+$`)

// ErrInvalidHandle, ErrMissingLabel are user-facing parse errors.
var (
	ErrInvalidHandle = errors.New("invalid handle")
	ErrMissingLabel  = errors.New("missing label")
)

// ParseHandle parses and validates a handle string. It does NOT check the
// provider registry — pass the result to ValidateProvider for that.
func ParseHandle(s string) (Handle, error) {
	if s == "" {
		return Handle{}, fmt.Errorf(`%w %q. Expected form: provider:label (e.g. google:work)`, ErrInvalidHandle, s)
	}
	if !strings.Contains(s, ":") {
		return Handle{}, fmt.Errorf("%w. Use provider:label form, e.g. apl login %s:work", ErrMissingLabel, s)
	}
	if !handleRe.MatchString(s) {
		return Handle{}, fmt.Errorf(`%w %q. Expected form: provider:label (e.g. google:work)`, ErrInvalidHandle, s)
	}
	idx := strings.Index(s, ":")
	return Handle{Provider: s[:idx], Label: s[idx+1:]}, nil
}

// ValidateProvider ensures the handle's provider exists in the registry.
func ValidateProvider(h Handle, reg *provider.Registry) error {
	if reg == nil {
		return fmt.Errorf(`unknown provider %q`, h.Provider)
	}
	if _, err := reg.Get(h.Provider); err != nil {
		return fmt.Errorf(`unknown provider %q. Known providers: %s`, h.Provider, strings.Join(reg.Names(), ", "))
	}
	return nil
}
