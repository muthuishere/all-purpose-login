package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/spf13/cobra"
)

// SetupOptions drive RunSetup. Tests inject fakes; production uses real shells.
type SetupOptions struct {
	Providers   []string // "google", "microsoft"; empty = both
	Reconfigure bool
	Reset       bool

	Shell     Shell
	Prompter  Prompter
	Validator Validator
	Stdout    io.Writer
	Stderr    io.Writer
}

// ExitCoded is an error that carries the CLI exit code the orchestrator should
// use when this error propagates to main.
type ExitCoded struct {
	Code int
	Err  error
}

func (e *ExitCoded) Error() string { return e.Err.Error() }
func (e *ExitCoded) Unwrap() error { return e.Err }

// ExitCodeFor maps a setup error to the documented exit code.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ec *ExitCoded
	if errors.As(err, &ec) {
		return ec.Code
	}
	switch {
	case errors.Is(err, ErrMissingCLI), errors.Is(err, ErrNotLoggedIn):
		return 2
	case errors.Is(err, ErrProviderFailure):
		return 3
	}
	return 1
}

// RunSetup orchestrates the bootstrapper. It loads the existing config,
// applies --reset / --reconfigure, runs provider flows, and atomically writes
// the merged config at the end.
func RunSetup(ctx context.Context, opts SetupOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Prompter == nil {
		opts.Prompter = NewStdPrompter(os.Stdin, opts.Stdout)
	}
	if opts.Validator == nil {
		opts.Validator = noopValidator{}
	}
	if opts.Shell == nil {
		opts.Shell = NewShell()
	}

	// Load current config (tolerate missing).
	cur, err := config.Load()
	if err != nil && !errors.Is(err, config.ErrNotConfigured) {
		return err
	}
	if cur == nil {
		cur = &config.Config{}
	}

	// Reset: wipe config first.
	if opts.Reset {
		if !opts.Prompter.Confirm("Delete existing config.yaml and start fresh?") {
			return &ExitCoded{Code: 1, Err: fmt.Errorf("aborted")}
		}
		cur = &config.Config{}
		if err := config.Save(cur); err != nil {
			return &ExitCoded{Code: 1, Err: fmt.Errorf("reset: %w", err)}
		}
	}

	providers := normalizeProviders(opts.Providers)
	if len(providers) == 0 {
		// Nothing to do (e.g. reset-only).
		return nil
	}

	// Idempotency: gather providers that are already configured.
	alreadyOK := map[string]bool{}
	for _, p := range providers {
		if isConfigured(cur, p) && !opts.Reconfigure {
			alreadyOK[p] = true
			fmt.Fprintf(opts.Stdout, "✓ %s already configured\n", displayName(p))
		}
	}
	allOK := true
	for _, p := range providers {
		if !alreadyOK[p] {
			allOK = false
			break
		}
	}
	if allOK {
		fmt.Fprintln(opts.Stdout, "Nothing to do. Use --reconfigure to re-run, or --reset to start over.")
		return nil
	}

	// Build the final config in memory; only save once at the very end.
	next := *cur

	for _, p := range providers {
		if alreadyOK[p] {
			continue
		}
		switch p {
		case "microsoft":
			pc, err := runMicrosoft(ctx, cur.Microsoft, opts.Shell, opts.Prompter, opts.Stdout, opts.Stderr)
			if err != nil {
				return err
			}
			next.Microsoft = pc
		case "google":
			pc, err := runGoogle(ctx, cur.Google, opts.Shell, opts.Prompter, opts.Validator, opts.Stdout, opts.Stderr)
			if err != nil {
				return err
			}
			next.Google = pc
		default:
			return fmt.Errorf("unknown provider %q", p)
		}
	}

	if err := config.Save(&next); err != nil {
		return &ExitCoded{Code: 1, Err: err}
	}
	return nil
}

func normalizeProviders(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	var out []string
	for _, p := range in {
		switch p {
		case "google":
			out = append(out, "google")
		case "microsoft", "ms":
			out = append(out, "microsoft")
		}
	}
	return out
}

func displayName(provider string) string {
	switch provider {
	case "microsoft":
		return "Microsoft"
	case "google":
		return "Google"
	}
	return provider
}

func isConfigured(cfg *config.Config, provider string) bool {
	switch provider {
	case "google":
		return cfg.Google.ClientID != "" && googleClientIDRe.MatchString(cfg.Google.ClientID)
	case "microsoft":
		return cfg.Microsoft.ClientID != ""
	}
	return false
}

// NewSetupCommand returns the cobra command tree for `apl setup`.
func NewSetupCommand() *cobra.Command {
	var reconfigure, reset bool

	mkRun := func(providers []string) func(cmd *cobra.Command, args []string) error {
		return func(cmd *cobra.Command, args []string) error {
			// When invoked at the top level (no subcommand), default = both.
			ps := providers
			if len(ps) == 0 {
				ps = []string{"google", "microsoft"}
			}
			return RunSetup(cmd.Context(), SetupOptions{
				Providers:   ps,
				Reconfigure: reconfigure,
				Reset:       reset,
				Stdout:      cmd.OutOrStdout(),
				Stderr:      cmd.ErrOrStderr(),
			})
		}
	}

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure OAuth clients for Google and Microsoft",
		RunE:  mkRun(nil),
	}
	cmd.PersistentFlags().BoolVar(&reconfigure, "reconfigure", false, "force re-prompting even if already configured")
	cmd.PersistentFlags().BoolVar(&reset, "reset", false, "wipe config.yaml then run full setup")

	cmd.AddCommand(&cobra.Command{
		Use:   "google",
		Short: "Configure Google OAuth client",
		RunE:  mkRun([]string{"google"}),
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "ms",
		Aliases: []string{"microsoft"},
		Short:   "Configure Microsoft OAuth client",
		RunE:    mkRun([]string{"microsoft"}),
	})

	return cmd
}

// --- StdPrompter (production) ------------------------------------------------

type stdPrompter struct {
	r *bufio.Reader
	w io.Writer
}

// NewStdPrompter builds a Prompter that reads lines from r and writes to w.
func NewStdPrompter(r io.Reader, w io.Writer) Prompter {
	return &stdPrompter{r: bufio.NewReader(r), w: w}
}

func (p *stdPrompter) Confirm(msg string) bool {
	fmt.Fprintf(p.w, "%s [y/N]: ", msg)
	line, _ := p.r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func (p *stdPrompter) Pick(msg string, options []string) int {
	fmt.Fprintln(p.w, msg)
	for i, o := range options {
		fmt.Fprintf(p.w, "  %d) %s\n", i+1, o)
	}
	fmt.Fprintf(p.w, "Choose [1]: ")
	line, _ := p.r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return 0
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return 0
	}
	return n - 1
}

func (p *stdPrompter) Input(msg string) string {
	fmt.Fprintf(p.w, "%s", msg)
	line, _ := p.r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func (p *stdPrompter) Wait(msg string) error {
	fmt.Fprintln(p.w, msg)
	_, err := p.r.ReadString('\n')
	return err
}

// noopValidator is the default validator used when none is injected; it passes
// through unconditionally. The real OAuth round-trip validator is wired at a
// higher level once internal/oauth is integrated.
type noopValidator struct{}

func (noopValidator) Validate(ctx context.Context, clientID string) error { return nil }
