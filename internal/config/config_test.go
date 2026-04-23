package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath_XDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(dir, "apl", "config.yaml")
	if got != want {
		t.Fatalf("DefaultPath = %q; want %q", got, want)
	}
}

func TestDefaultPath_HomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".config", "apl", "config.yaml")
	if got != want {
		t.Fatalf("DefaultPath = %q; want %q", got, want)
	}
}

func TestLoad_MissingFile_ReturnsErrNotConfigured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_, err := Load()
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured; got %v", err)
	}
}

func TestLoad_WellFormedYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	aplDir := filepath.Join(dir, "apl")
	if err := os.MkdirAll(aplDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(aplDir, "config.yaml")
	yamlBody := `google:
  client_id: "goog-id"
  project_id: "proj-1"
microsoft:
  client_id: "ms-id"
  tenant: "common"
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Google.ClientID != "goog-id" || cfg.Google.ProjectID != "proj-1" {
		t.Errorf("google mismatch: %+v", cfg.Google)
	}
	if cfg.Microsoft.ClientID != "ms-id" || cfg.Microsoft.Tenant != "common" {
		t.Errorf("microsoft mismatch: %+v", cfg.Microsoft)
	}
}

func TestLoad_CorruptYAML_ReturnsWrappedParseError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	aplDir := filepath.Join(dir, "apl")
	if err := os.MkdirAll(aplDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(aplDir, "config.yaml")
	if err := os.WriteFile(path, []byte("google: [this is : not valid yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("should not be ErrNotConfigured; got %v", err)
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("expected wrapped config error; got %v", err)
	}
}

func TestSave_CreatesParentDir0700_File0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := &Config{
		Google:    ProviderConfig{ClientID: "gid", ProjectID: "pid"},
		Microsoft: ProviderConfig{ClientID: "mid", Tenant: "common"},
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	aplDir := filepath.Join(dir, "apl")
	dirInfo, err := os.Stat(aplDir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o; want 0700", dirInfo.Mode().Perm())
	}
	path := filepath.Join(aplDir, "config.yaml")
	fInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if fInfo.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o; want 0600", fInfo.Mode().Perm())
	}
}

func TestSave_Load_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := &Config{
		Google:    ProviderConfig{ClientID: "gid", ProjectID: "pid"},
		Microsoft: ProviderConfig{ClientID: "mid", Tenant: "common"},
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Google != cfg.Google || got.Microsoft != cfg.Microsoft {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, cfg)
	}
}

func TestSave_AtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := &Config{Google: ProviderConfig{ClientID: "gid"}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	aplDir := filepath.Join(dir, "apl")
	entries, err := os.ReadDir(aplDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	// Exactly one file: config.yaml.
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Errorf("unexpected entries: %v", entries)
	}
}
