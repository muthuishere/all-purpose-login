package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ErrNotConfigured is returned by Load when the config file does not exist.
var ErrNotConfigured = errors.New("config: not configured (run `apl setup`)")

type ProviderConfig struct {
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	ProjectID    string `yaml:"project_id,omitempty"`
	Tenant       string `yaml:"tenant,omitempty"`
}

// Config holds OAuth client configuration keyed by label, per provider.
// Each label corresponds to a handle suffix, e.g. cfg.Google["deemwar"]
// is the OAuth client used by `apl login google:deemwar`.
type Config struct {
	Google    map[string]ProviderConfig `yaml:"google,omitempty"`
	Microsoft map[string]ProviderConfig `yaml:"microsoft,omitempty"`
}

// GetProvider returns the per-label config for a provider, or false if absent.
func (c *Config) GetProvider(provider, label string) (ProviderConfig, bool) {
	if c == nil {
		return ProviderConfig{}, false
	}
	m := c.providerMap(provider)
	if m == nil {
		return ProviderConfig{}, false
	}
	pc, ok := m[label]
	return pc, ok
}

// SetProvider writes the per-label config for a provider, allocating the map
// if needed.
func (c *Config) SetProvider(provider, label string, pc ProviderConfig) {
	switch provider {
	case "google":
		if c.Google == nil {
			c.Google = make(map[string]ProviderConfig)
		}
		c.Google[label] = pc
	case "ms", "microsoft":
		if c.Microsoft == nil {
			c.Microsoft = make(map[string]ProviderConfig)
		}
		c.Microsoft[label] = pc
	}
}

// Labels returns the configured labels for a provider, sorted.
func (c *Config) Labels(provider string) []string {
	m := c.providerMap(provider)
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable order; small N so a sort.Strings is fine
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// AnyLabel returns one configured label for a provider (deterministic-ish:
// the first in sorted order), or "" if none.
func (c *Config) AnyLabel(provider string) string {
	labels := c.Labels(provider)
	if len(labels) == 0 {
		return ""
	}
	return labels[0]
}

func (c *Config) providerMap(provider string) map[string]ProviderConfig {
	switch provider {
	case "google":
		return c.Google
	case "ms", "microsoft":
		return c.Microsoft
	}
	return nil
}

// DefaultPath returns $XDG_CONFIG_HOME/apl/config.yaml, falling back to
// $HOME/.config/apl/config.yaml.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "apl", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "apl", "config.yaml"), nil
}

// Load reads the config file from DefaultPath.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotConfigured
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes cfg atomically to DefaultPath with file mode 0600,
// creating the parent directory with mode 0700 if needed.
func Save(cfg *Config) error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("config: chmod %s: %w", dir, err)
	}

	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	tmp := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("config: create temp: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("config: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("config: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: close temp: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: chmod temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}
