// Package setup implements a resumable, LLM-driveable state machine for
// configuring per-label OAuth clients with Google and Microsoft.
//
// State is persisted to ~/.config/apl/setup-state/<provider>-<label>.yaml
// after every step transition. An NDJSON emitter writes one event per line
// to stdout so an LLM (or shell script) can stream events, suspend at human
// hand-off points, and resume by re-invoking the CLI with --resume plus any
// required input flags.
package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Status values for a step.
const (
	StatusPending       = "pending"
	StatusInProgress    = "in_progress"
	StatusDone          = "done"
	StatusAwaitingHuman = "awaiting_human"
	StatusAwaitingInput = "awaiting_input"
	StatusFailed        = "failed"
)

// State is the persisted setup journal.
type State struct {
	Provider  string                `yaml:"provider"`
	Label     string                `yaml:"label"`
	StartedAt time.Time             `yaml:"started_at"`
	UpdatedAt time.Time             `yaml:"updated_at"`
	Steps     map[string]*StepState `yaml:"steps,omitempty"`
	Inputs    map[string]string     `yaml:"inputs,omitempty"`
}

// StepState records a single step's progress.
type StepState struct {
	Status    string            `yaml:"status"`
	Output    map[string]string `yaml:"output,omitempty"`
	Reason    string            `yaml:"reason,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Message   string            `yaml:"message,omitempty"`
	Fields    []string          `yaml:"fields,omitempty"`
	StartedAt time.Time         `yaml:"started_at,omitempty"`
	UpdatedAt time.Time         `yaml:"updated_at,omitempty"`
}

// ErrNoState is returned by Load when no state file exists for (provider,label).
var ErrNoState = errors.New("setup: no state for this provider:label (run setup without --resume to start)")

// StatePath returns the canonical state-file path for a (provider,label).
func StatePath(provider, label string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s.yaml", provider, label)), nil
}

func stateDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "apl", "setup-state"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("setup: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "apl", "setup-state"), nil
}

// Load reads state from disk; returns ErrNoState if no file exists.
func Load(provider, label string) (*State, error) {
	path, err := StatePath(provider, label)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoState
		}
		return nil, fmt.Errorf("setup: read %s: %w", path, err)
	}
	var s State
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("setup: parse %s: %w", path, err)
	}
	if s.Steps == nil {
		s.Steps = map[string]*StepState{}
	}
	if s.Inputs == nil {
		s.Inputs = map[string]string{}
	}
	return &s, nil
}

// Save writes state atomically to disk with mode 0600.
func Save(s *State) error {
	if s == nil {
		return errors.New("setup: nil state")
	}
	s.UpdatedAt = time.Now().UTC()
	path, err := StatePath(s.Provider, s.Label)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("setup: mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("setup: chmod %s: %w", dir, err)
	}
	body, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("setup: marshal: %w", err)
	}
	tmp := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("setup: create temp: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("setup: write temp: %w", err)
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
	return os.Rename(tmp, path)
}

// Reset removes the state file (no error if absent).
func Reset(provider, label string) error {
	path, err := StatePath(provider, label)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// New initializes a fresh state.
func New(provider, label string) *State {
	now := time.Now().UTC()
	return &State{
		Provider:  provider,
		Label:     label,
		StartedAt: now,
		UpdatedAt: now,
		Steps:     map[string]*StepState{},
		Inputs:    map[string]string{},
	}
}

// StepStatus returns the recorded status for a step (or pending if absent).
func (s *State) StepStatus(id string) string {
	if s.Steps == nil {
		return StatusPending
	}
	st, ok := s.Steps[id]
	if !ok || st == nil {
		return StatusPending
	}
	return st.Status
}

// SetStep records a step's outcome and persists.
func (s *State) SetStep(id string, st *StepState) {
	if s.Steps == nil {
		s.Steps = map[string]*StepState{}
	}
	now := time.Now().UTC()
	if existing, ok := s.Steps[id]; ok && !existing.StartedAt.IsZero() {
		st.StartedAt = existing.StartedAt
	} else {
		st.StartedAt = now
	}
	st.UpdatedAt = now
	s.Steps[id] = st
}

// SetInput records a collected input value.
func (s *State) SetInput(field, value string) {
	if s.Inputs == nil {
		s.Inputs = map[string]string{}
	}
	s.Inputs[field] = value
}

// Input returns a recorded input by field name.
func (s *State) Input(field string) string {
	if s.Inputs == nil {
		return ""
	}
	return s.Inputs[field]
}
