package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

// Shell is an abstraction over exec.Command so setup flows can be unit-tested
// without shelling out to az / gcloud.
type Shell interface {
	// Run executes name with args, returning stdout, stderr, and any error.
	Run(ctx context.Context, name string, args ...string) (stdout string, stderr string, err error)
	// RunInteractive executes name with args, wiring the subprocess's stdio to
	// the parent process's real stdin/stdout/stderr so interactive tools
	// (gcloud auth login, az login) can prompt the user and open a browser.
	RunInteractive(ctx context.Context, name string, args ...string) error
	// Available reports whether name is resolvable on PATH.
	Available(name string) bool
}

// realShell is the production Shell backed by os/exec.
type realShell struct{}

// NewShell returns a Shell that invokes real subprocesses.
func NewShell() Shell { return realShell{} }

func (realShell) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (realShell) RunInteractive(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (realShell) Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// --- Prompter ---------------------------------------------------------------

// Prompter abstracts user interaction so flows can be unit-tested.
type Prompter interface {
	Confirm(msg string) bool
	Pick(msg string, options []string) int
	Input(msg string) string
	Wait(msg string) error
}

// --- Validator --------------------------------------------------------------

// Validator abstracts the Google Client-ID OAuth round-trip.
type Validator interface {
	Validate(ctx context.Context, clientID string) error
}
