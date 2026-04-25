package setup

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStatePath_XDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := StatePath("google", "deemwar")
	if err != nil {
		t.Fatalf("StatePath: %v", err)
	}
	want := filepath.Join(dir, "apl", "setup-state", "google-deemwar.yaml")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	st := New("google", "deemwar")
	st.SetStep("account", &StepState{
		Status: StatusDone,
		Output: map[string]string{"account": "admin@deemwar.com"},
	})
	st.SetStep("project", &StepState{
		Status:  StatusAwaitingHuman,
		URL:     "https://console.cloud.google.com/auth/overview?project=apl-x",
		Message: "Open and click CREATE",
	})
	st.SetInput("project_id", "apl-x")

	if err := Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load("google", "deemwar")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Provider != "google" || got.Label != "deemwar" {
		t.Errorf("provider/label mismatch: %+v", got)
	}
	if got.StepStatus("account") != StatusDone {
		t.Errorf("account status = %q", got.StepStatus("account"))
	}
	if got.Steps["project"].URL == "" {
		t.Errorf("project URL not persisted")
	}
	if got.Input("project_id") != "apl-x" {
		t.Errorf("input not persisted: %v", got.Inputs)
	}
}

func TestLoad_Missing_ReturnsErrNoState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := Load("google", "absent")
	if !errors.Is(err, ErrNoState) {
		t.Errorf("expected ErrNoState, got %v", err)
	}
}

func TestReset_RemovesStateFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st := New("ms", "x")
	if err := Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Reset("ms", "x"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := Load("ms", "x"); !errors.Is(err, ErrNoState) {
		t.Errorf("expected ErrNoState after Reset, got %v", err)
	}
}
