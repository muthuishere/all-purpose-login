package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ActionKind classifies what a Step wants the runner to do next.
type ActionKind int

const (
	ActionDone ActionKind = iota
	ActionAwaitHuman
	ActionAwaitInput
	ActionFailed
)

// Action is the value returned by Step.Run.
type Action struct {
	Kind        ActionKind
	URL         string
	Message     string
	Fields      []string          // required input fields when Kind==ActionAwaitInput
	Output      map[string]string // step output, persisted into StepState
	FailReason  string
	Recoverable bool // for ActionFailed; if true, --resume can retry
}

// Step is a single setup action. Implementations are expected to be idempotent
// when their state is already StatusDone (the runner skips them in that case).
type Step interface {
	ID() string
	Run(ctx context.Context, state *State, env Env) (Action, error)
}

// Env carries dependencies into steps.
type Env struct {
	Shell      Shell
	Validator  Validator
	Stdout     io.Writer
	Stderr     io.Writer
	OpenURL    func(url string) error // may be nil; LLM-driven sessions don't open browsers
	ResumeCmd  string                 // textual "apl setup ... --resume ..." hint
	Provider   string                 // "google" | "ms"
	Label      string
	Inputs     map[string]string // CLI-provided inputs (account, project_id, etc.)
	Reuse      bool              // if true, prefer reuse-existing-project branch
}

// Shell mirrors cli.Shell so we don't import cli (which would cycle).
type Shell interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
	RunInteractive(ctx context.Context, name string, args ...string) error
	Available(name string) bool
}

// Validator is identical to cli.Validator.
type Validator interface {
	Validate(ctx context.Context, clientID string) error
}

// Runner executes steps in order, persisting state between each.
type Runner struct {
	Steps   []Step
	Emitter Emitter
}

// ErrSuspended is returned by Run when a step requested awaiting_* — the
// process should exit with a non-zero suspend code (or zero, depending on
// the harness). The state has been persisted and is resumable.
var ErrSuspended = errors.New("setup: suspended awaiting human or input")

// Run drives the step list to completion or first suspension/failure.
//
// On resume, a step previously suspended in StatusAwaitingHuman is treated
// as completed — the human is presumed to have done the action between runs.
// A step in StatusAwaitingInput is re-run only if its required input field
// has now been supplied; otherwise the step suspends again.
func (r *Runner) Run(ctx context.Context, state *State, env Env) error {
	for _, step := range r.Steps {
		id := step.ID()
		switch state.StepStatus(id) {
		case StatusDone:
			continue
		case StatusAwaitingHuman:
			// Resume past a human hand-off: treat as done.
			state.SetStep(id, &StepState{Status: StatusDone})
			if err := Save(state); err != nil {
				return fmt.Errorf("save state: %w", err)
			}
			r.Emitter.StepDone(id, nil)
			continue
		case StatusAwaitingInput:
			prev := state.Steps[id]
			haveAll := true
			for _, f := range prev.Fields {
				if env.Inputs[f] == "" {
					haveAll = false
					break
				}
			}
			if !haveAll {
				// Re-emit the await event without re-running the step body.
				r.Emitter.AwaitingInput(id, prev.URL, prev.Message, prev.Fields, env.ResumeCmd)
				return ErrSuspended
			}
			// Inputs supplied → fall through to re-run the step.
		}
		state.SetStep(id, &StepState{Status: StatusInProgress})
		if err := Save(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		r.Emitter.StepStarted(id)

		act, err := step.Run(ctx, state, env)
		if err != nil {
			state.SetStep(id, &StepState{Status: StatusFailed, Reason: err.Error()})
			_ = Save(state)
			r.Emitter.StepFailed(id, err.Error(), false)
			return err
		}

		switch act.Kind {
		case ActionDone:
			state.SetStep(id, &StepState{Status: StatusDone, Output: act.Output})
			if err := Save(state); err != nil {
				return fmt.Errorf("save state: %w", err)
			}
			r.Emitter.StepDone(id, act.Output)
		case ActionAwaitHuman:
			state.SetStep(id, &StepState{
				Status:  StatusAwaitingHuman,
				URL:     act.URL,
				Message: act.Message,
			})
			_ = Save(state)
			r.Emitter.AwaitingHuman(id, act.URL, act.Message, env.ResumeCmd)
			return ErrSuspended
		case ActionAwaitInput:
			state.SetStep(id, &StepState{
				Status:  StatusAwaitingInput,
				URL:     act.URL,
				Message: act.Message,
				Fields:  act.Fields,
			})
			_ = Save(state)
			r.Emitter.AwaitingInput(id, act.URL, act.Message, act.Fields, env.ResumeCmd)
			return ErrSuspended
		case ActionFailed:
			state.SetStep(id, &StepState{Status: StatusFailed, Reason: act.FailReason})
			_ = Save(state)
			r.Emitter.StepFailed(id, act.FailReason, act.Recoverable)
			if act.Recoverable {
				return ErrSuspended
			}
			return fmt.Errorf("step %s failed: %s", id, act.FailReason)
		}
	}
	// All steps done.
	summary := map[string]string{}
	for _, step := range r.Steps {
		if st, ok := state.Steps[step.ID()]; ok {
			for k, v := range st.Output {
				summary[k] = v
			}
		}
	}
	r.Emitter.Completed(state.Label, summary)
	return nil
}
