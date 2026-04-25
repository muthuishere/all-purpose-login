package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/setup"
)

// LLMSetupOptions configure the resumable, state-machine-driven setup path.
// Flags --json, --resume, --status, --reset-state activate this path.
type LLMSetupOptions struct {
	Provider string // "google" | "ms"
	Label    string
	JSON     bool
	Resume   bool
	Status   bool
	ResetSt  bool // wipe state file (distinct from existing --reset which wipes config)
	Reuse    string

	Inputs map[string]string

	Shell     Shell
	Validator Validator
	Stdout    io.Writer
	Stderr    io.Writer
}

// RunLLMSetup dispatches to the state-machine runner. The caller configures
// inputs from CLI flags (--account, --project-id, --client-secret-file, etc.)
// and pipes Stdout (for --json this is NDJSON).
func RunLLMSetup(ctx context.Context, opts LLMSetupOptions) error {
	if opts.Provider != "google" && opts.Provider != "ms" {
		return userErr("--provider must be google or ms")
	}
	if opts.Label == "" {
		return userErr("--label is required")
	}

	// --status: print state file as YAML/NDJSON-event and return.
	if opts.Status {
		st, err := setup.Load(opts.Provider, opts.Label)
		if err != nil {
			if errors.Is(err, setup.ErrNoState) {
				fmt.Fprintln(opts.Stdout, "no state for this provider:label")
				return nil
			}
			return netErr("%s", err.Error())
		}
		path, _ := setup.StatePath(opts.Provider, opts.Label)
		fmt.Fprintf(opts.Stdout, "state file: %s\n", path)
		fmt.Fprintf(opts.Stdout, "started: %s\n", st.StartedAt.Format("2006-01-02T15:04:05Z"))
		for id, ss := range st.Steps {
			fmt.Fprintf(opts.Stdout, "  %s: %s", id, ss.Status)
			if ss.URL != "" {
				fmt.Fprintf(opts.Stdout, "  url=%s", ss.URL)
			}
			if ss.Reason != "" {
				fmt.Fprintf(opts.Stdout, "  reason=%s", ss.Reason)
			}
			fmt.Fprintln(opts.Stdout)
		}
		return nil
	}

	// --reset-state: wipe state file.
	if opts.ResetSt {
		if err := setup.Reset(opts.Provider, opts.Label); err != nil {
			return netErr("reset state: %s", err.Error())
		}
		fmt.Fprintln(opts.Stdout, "state file removed")
		return nil
	}

	// Load or initialize state.
	var st *setup.State
	loaded, err := setup.Load(opts.Provider, opts.Label)
	switch {
	case err == nil && opts.Resume:
		st = loaded
	case err == nil && !opts.Resume:
		// Fresh run requested but state exists — reuse if all-pending; otherwise start over.
		st = setup.New(opts.Provider, opts.Label)
	case errors.Is(err, setup.ErrNoState):
		st = setup.New(opts.Provider, opts.Label)
	default:
		return netErr("load state: %s", err.Error())
	}

	// Merge CLI inputs into state inputs (CLI flags take precedence).
	for k, v := range opts.Inputs {
		if v != "" {
			st.SetInput(k, v)
		}
	}

	// Pick emitter.
	var em setup.Emitter
	if opts.JSON {
		em = setup.NewJSONEmitter(opts.Stdout)
	} else {
		em = setup.NewInteractiveEmitter(opts.Stdout)
	}

	// Build step list.
	var steps []setup.Step
	switch opts.Provider {
	case "google":
		// Reuse path: if --reuse=<existing-label> is set, copy that label's
		// project_id + client from cfg and run the abbreviated step list.
		var rPID, rCID, rSec string
		if opts.Reuse != "" {
			cfg, _ := config.Load()
			if cfg != nil {
				if pc, ok := cfg.GetProvider("google", opts.Reuse); ok {
					rPID = pc.ProjectID
					rCID = pc.ClientID
					rSec = pc.ClientSecret
				}
			}
			if rPID == "" || rCID == "" {
				return userErr("--reuse=%s: no existing google config under that label", opts.Reuse)
			}
		}
		steps = setup.GoogleSteps(rPID, rCID, rSec)
	case "ms":
		steps = setup.MicrosoftSteps()
	}

	env := setup.Env{
		Shell:     shellAdapter{opts.Shell},
		Validator: validatorAdapter{opts.Validator},
		Stdout:    opts.Stdout,
		Stderr:    opts.Stderr,
		Provider:  opts.Provider,
		Label:     opts.Label,
		Inputs:    st.Inputs,
		ResumeCmd: buildResumeCmd(opts),
		Reuse:     opts.Reuse != "",
	}

	r := &setup.Runner{Steps: steps, Emitter: em}
	if err := r.Run(ctx, st, env); err != nil {
		if errors.Is(err, setup.ErrSuspended) {
			return &ExitCoded{Code: 75, Err: err} // EX_TEMPFAIL — re-runnable
		}
		return err
	}

	// Completion: persist final config from state outputs.
	if err := persistFinalConfig(opts.Provider, opts.Label, st); err != nil {
		return netErr("persist config: %s", err.Error())
	}
	// Cleanup: remove state file on success.
	_ = setup.Reset(opts.Provider, opts.Label)
	return nil
}

// persistFinalConfig walks step outputs to build a config.ProviderConfig and
// writes it to the user's config.yaml under (provider, label).
func persistFinalConfig(provider, label string, st *setup.State) error {
	pc := config.ProviderConfig{}
	for _, step := range st.Steps {
		for k, v := range step.Output {
			switch k {
			case "client_id":
				pc.ClientID = v
			case "client_secret":
				pc.ClientSecret = v
			case "project_id":
				pc.ProjectID = v
			case "tenant":
				pc.Tenant = v
			case "app_id":
				if pc.ClientID == "" {
					pc.ClientID = v
				}
			}
		}
	}
	if pc.ClientID == "" {
		return fmt.Errorf("no client_id in completed state — setup incomplete")
	}
	cfg, err := config.Load()
	if err != nil && !errors.Is(err, config.ErrNotConfigured) {
		return err
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.SetProvider(provider, label, pc)
	return config.Save(cfg)
}

func buildResumeCmd(opts LLMSetupOptions) string {
	parts := []string{"apl", "setup", opts.Provider, "--label", opts.Label, "--resume"}
	if opts.JSON {
		parts = append(parts, "--json")
	}
	return strings.Join(parts, " ")
}

// shellAdapter adapts cli.Shell to setup.Shell (identical interface, needed
// because Go does not allow defining a local interface alias on an external
// type).
type shellAdapter struct{ s Shell }

func (a shellAdapter) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if a.s == nil {
		a.s = NewShell()
	}
	return a.s.Run(ctx, name, args...)
}
func (a shellAdapter) RunInteractive(ctx context.Context, name string, args ...string) error {
	if a.s == nil {
		a.s = NewShell()
	}
	return a.s.RunInteractive(ctx, name, args...)
}
func (a shellAdapter) Available(name string) bool {
	if a.s == nil {
		a.s = NewShell()
	}
	return a.s.Available(name)
}

type validatorAdapter struct{ v Validator }

func (a validatorAdapter) Validate(ctx context.Context, clientID string) error {
	if a.v == nil {
		return nil
	}
	return a.v.Validate(ctx, clientID)
}
