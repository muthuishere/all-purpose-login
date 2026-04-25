package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// stubStep is a controllable Step used in runner tests.
type stubStep struct {
	id  string
	act Action
	err error
}

func (s *stubStep) ID() string { return s.id }
func (s *stubStep) Run(ctx context.Context, st *State, env Env) (Action, error) {
	return s.act, s.err
}

func TestRunner_AllDone_EmitsCompleted(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st := New("google", "x")
	var buf bytes.Buffer
	em := NewJSONEmitter(&buf)
	r := &Runner{
		Emitter: em,
		Steps: []Step{
			&stubStep{id: "a", act: Action{Kind: ActionDone, Output: map[string]string{"k": "v"}}},
			&stubStep{id: "b", act: Action{Kind: ActionDone}},
		},
	}
	if err := r.Run(context.Background(), st, Env{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"event":"completed"`) {
		t.Errorf("expected completed event, got:\n%s", out)
	}
	if st.StepStatus("a") != StatusDone || st.StepStatus("b") != StatusDone {
		t.Errorf("expected both steps done, got a=%s b=%s", st.StepStatus("a"), st.StepStatus("b"))
	}
}

func TestRunner_AwaitHuman_SuspendsAndPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st := New("google", "x")
	var buf bytes.Buffer
	r := &Runner{
		Emitter: NewJSONEmitter(&buf),
		Steps: []Step{
			&stubStep{id: "a", act: Action{Kind: ActionDone}},
			&stubStep{id: "b", act: Action{
				Kind:    ActionAwaitHuman,
				URL:     "https://console.cloud.google.com/auth/overview?project=apl-x",
				Message: "Open and click CREATE",
			}},
			&stubStep{id: "c", act: Action{Kind: ActionDone}},
		},
	}
	err := r.Run(context.Background(), st, Env{ResumeCmd: "apl setup google --label x --resume --json"})
	if !errors.Is(err, ErrSuspended) {
		t.Fatalf("expected ErrSuspended, got %v", err)
	}
	// Decode each line.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var hasAwait bool
	for _, ln := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("non-JSON line: %q", ln)
		}
		if ev["event"] == "awaiting_human" {
			hasAwait = true
			if ev["url"] == "" || ev["instructions"] == "" || ev["resume_command"] == "" {
				t.Errorf("awaiting_human missing fields: %v", ev)
			}
		}
	}
	if !hasAwait {
		t.Errorf("no awaiting_human event in output")
	}

	// Reload state and confirm persistence.
	loaded, err := Load("google", "x")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.StepStatus("b") != StatusAwaitingHuman {
		t.Errorf("b status = %q; want awaiting_human", loaded.StepStatus("b"))
	}
	if loaded.StepStatus("c") != StatusPending {
		t.Errorf("c status = %q; want pending (not yet run)", loaded.StepStatus("c"))
	}
}

func TestRunner_Resume_SkipsDoneSteps(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st := New("google", "x")
	st.SetStep("a", &StepState{Status: StatusDone})
	if err := Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var ranA, ranB int
	steps := []Step{
		&stubStep{id: "a", act: Action{Kind: ActionDone}},
		&stubStep{id: "b", act: Action{Kind: ActionDone}},
	}
	wrap := func(orig *stubStep, counter *int) Step {
		return &countingStep{stubStep: orig, n: counter}
	}
	steps[0] = wrap(steps[0].(*stubStep), &ranA)
	steps[1] = wrap(steps[1].(*stubStep), &ranB)

	loaded, _ := Load("google", "x")
	r := &Runner{Emitter: NewJSONEmitter(&bytes.Buffer{}), Steps: steps}
	if err := r.Run(context.Background(), loaded, Env{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ranA != 0 {
		t.Errorf("step a should have been skipped (already done), ran %d times", ranA)
	}
	if ranB != 1 {
		t.Errorf("step b should have run once, ran %d", ranB)
	}
}

type countingStep struct {
	*stubStep
	n *int
}

func (c *countingStep) Run(ctx context.Context, st *State, env Env) (Action, error) {
	*c.n++
	return c.stubStep.Run(ctx, st, env)
}
