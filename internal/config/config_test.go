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
  muthuishere:
    client_id: "goog-id"
    project_id: "proj-1"
  deemwar:
    client_id: "goog-id-2"
    project_id: "proj-2"
microsoft:
  reqsume:
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
	pc, ok := cfg.GetProvider("google", "muthuishere")
	if !ok || pc.ClientID != "goog-id" || pc.ProjectID != "proj-1" {
		t.Errorf("google[muthuishere] mismatch: %+v ok=%v", pc, ok)
	}
	pc, ok = cfg.GetProvider("google", "deemwar")
	if !ok || pc.ClientID != "goog-id-2" {
		t.Errorf("google[deemwar] mismatch: %+v ok=%v", pc, ok)
	}
	pc, ok = cfg.GetProvider("ms", "reqsume")
	if !ok || pc.ClientID != "ms-id" || pc.Tenant != "common" {
		t.Errorf("ms[reqsume] mismatch: %+v ok=%v", pc, ok)
	}
}

func TestGetProvider_MissingLabel(t *testing.T) {
	cfg := &Config{Google: map[string]ProviderConfig{"a": {ClientID: "x"}}}
	if _, ok := cfg.GetProvider("google", "missing"); ok {
		t.Errorf("expected missing label to return ok=false")
	}
	if _, ok := cfg.GetProvider("nosuch", "a"); ok {
		t.Errorf("expected unknown provider to return ok=false")
	}
}

func TestSetProvider_AllocatesMap(t *testing.T) {
	cfg := &Config{}
	cfg.SetProvider("google", "deemwar", ProviderConfig{ClientID: "x"})
	pc, ok := cfg.GetProvider("google", "deemwar")
	if !ok || pc.ClientID != "x" {
		t.Errorf("set/get mismatch: %+v ok=%v", pc, ok)
	}
	cfg.SetProvider("ms", "reqsume", ProviderConfig{ClientID: "y", Tenant: "common"})
	pc, ok = cfg.GetProvider("ms", "reqsume")
	if !ok || pc.ClientID != "y" {
		t.Errorf("ms set/get mismatch: %+v ok=%v", pc, ok)
	}
}

func TestLabels_Sorted(t *testing.T) {
	cfg := &Config{Google: map[string]ProviderConfig{
		"zebra":  {},
		"alpha":  {},
		"middle": {},
	}}
	got := cfg.Labels("google")
	want := []string{"alpha", "middle", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len = %d; want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Labels[%d] = %q; want %q", i, got[i], want[i])
		}
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
	cfg := &Config{}
	cfg.SetProvider("google", "muthuishere", ProviderConfig{ClientID: "gid", ProjectID: "pid"})
	cfg.SetProvider("ms", "reqsume", ProviderConfig{ClientID: "mid", Tenant: "common"})
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
	cfg := &Config{}
	cfg.SetProvider("google", "muthuishere", ProviderConfig{ClientID: "gid", ProjectID: "pid"})
	cfg.SetProvider("ms", "reqsume", ProviderConfig{ClientID: "mid", Tenant: "common"})
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gpc, _ := got.GetProvider("google", "muthuishere")
	wpc, _ := cfg.GetProvider("google", "muthuishere")
	if gpc != wpc {
		t.Errorf("google round-trip mismatch:\n got=%+v\nwant=%+v", gpc, wpc)
	}
	mpc, _ := got.GetProvider("ms", "reqsume")
	wmpc, _ := cfg.GetProvider("ms", "reqsume")
	if mpc != wmpc {
		t.Errorf("ms round-trip mismatch:\n got=%+v\nwant=%+v", mpc, wmpc)
	}
}

func TestSave_AtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := &Config{}
	cfg.SetProvider("google", "x", ProviderConfig{ClientID: "gid"})
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
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Errorf("unexpected entries: %v", entries)
	}
}
