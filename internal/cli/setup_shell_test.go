package cli

import (
	"context"
	"errors"
	"testing"
)

// fakeShell is a test double that records calls and returns canned responses
// keyed by command name and first arg.
type fakeShell struct {
	// calls records every invocation in order.
	calls []fakeCall
	// responses maps a matcher to a canned response. The matcher is
	// name + " " + args[0] when args present, else name.
	responses map[string]fakeResp
	// availableCmds is the set of commands that resolve on PATH.
	availableCmds map[string]bool
}

type fakeCall struct {
	Name string
	Args []string
}

type fakeResp struct {
	Stdout string
	Stderr string
	Err    error
}

func newFakeShell() *fakeShell {
	return &fakeShell{
		responses:     map[string]fakeResp{},
		availableCmds: map[string]bool{},
	}
}

func (f *fakeShell) respond(key string, r fakeResp) { f.responses[key] = r }
func (f *fakeShell) setAvailable(name string, ok bool) {
	f.availableCmds[name] = ok
}

func (f *fakeShell) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, fakeCall{Name: name, Args: append([]string(nil), args...)})
	// prefer longer keys: name+a0+a1+a2 → name+a0+a1 → name+a0 → name.
	if len(args) >= 3 {
		if r, ok := f.responses[name+" "+args[0]+" "+args[1]+" "+args[2]]; ok {
			return r.Stdout, r.Stderr, r.Err
		}
	}
	if len(args) >= 2 {
		if r, ok := f.responses[name+" "+args[0]+" "+args[1]]; ok {
			return r.Stdout, r.Stderr, r.Err
		}
	}
	if len(args) >= 1 {
		if r, ok := f.responses[name+" "+args[0]]; ok {
			return r.Stdout, r.Stderr, r.Err
		}
	}
	if r, ok := f.responses[name]; ok {
		return r.Stdout, r.Stderr, r.Err
	}
	return "", "", nil
}

func (f *fakeShell) Available(name string) bool {
	ok, seen := f.availableCmds[name]
	if !seen {
		return true // default: available if not configured
	}
	return ok
}

// findCall returns the first recorded call matching name (optionally first arg).
func (f *fakeShell) findCall(name string, firstArg string) *fakeCall {
	for i := range f.calls {
		c := &f.calls[i]
		if c.Name != name {
			continue
		}
		if firstArg != "" && (len(c.Args) == 0 || c.Args[0] != firstArg) {
			continue
		}
		return c
	}
	return nil
}

// hasArg returns true if args contains s.
func hasArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func TestFakeShell_RecordsCalls(t *testing.T) {
	fs := newFakeShell()
	_, _, _ = fs.Run(context.Background(), "az", "account", "show")
	if len(fs.calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(fs.calls))
	}
	if fs.calls[0].Name != "az" || fs.calls[0].Args[0] != "account" {
		t.Fatalf("bad recorded call: %+v", fs.calls[0])
	}
}

func TestFakeShell_CannedResponses(t *testing.T) {
	fs := newFakeShell()
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"x"}}`, Err: nil})
	fs.respond("gcloud", fakeResp{Stderr: "boom", Err: errors.New("fail")})

	out, _, err := fs.Run(context.Background(), "az", "account", "show")
	if err != nil || out != `{"user":{"name":"x"}}` {
		t.Fatalf("az: got out=%q err=%v", out, err)
	}
	_, serr, err := fs.Run(context.Background(), "gcloud", "projects", "list")
	if err == nil || serr != "boom" {
		t.Fatalf("gcloud: got serr=%q err=%v", serr, err)
	}
}

func TestFakeShell_Available(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", false)
	fs.setAvailable("gcloud", true)
	if fs.Available("az") {
		t.Fatal("az should be unavailable")
	}
	if !fs.Available("gcloud") {
		t.Fatal("gcloud should be available")
	}
	if !fs.Available("unset") {
		t.Fatal("unset commands default to available in the fake")
	}
}

// --- fakePrompter -----------------------------------------------------------

type fakePrompter struct {
	confirms []bool
	picks    []int
	inputs   []string
	waits    []error

	confirmCalls []string
	pickCalls    []string
	inputCalls   []string
	waitCalls    []string
}

func (p *fakePrompter) Confirm(msg string) bool {
	p.confirmCalls = append(p.confirmCalls, msg)
	if len(p.confirms) == 0 {
		return false
	}
	v := p.confirms[0]
	p.confirms = p.confirms[1:]
	return v
}

func (p *fakePrompter) Pick(msg string, options []string) int {
	p.pickCalls = append(p.pickCalls, msg)
	if len(p.picks) == 0 {
		return 0
	}
	v := p.picks[0]
	p.picks = p.picks[1:]
	return v
}

func (p *fakePrompter) Input(msg string) string {
	p.inputCalls = append(p.inputCalls, msg)
	if len(p.inputs) == 0 {
		return ""
	}
	v := p.inputs[0]
	p.inputs = p.inputs[1:]
	return v
}

func (p *fakePrompter) Wait(msg string) error {
	p.waitCalls = append(p.waitCalls, msg)
	if len(p.waits) == 0 {
		return nil
	}
	v := p.waits[0]
	p.waits = p.waits[1:]
	return v
}

// fakeValidator simulates the OAuth round-trip.
type fakeValidator struct {
	calls  []string
	errors []error
}

func (v *fakeValidator) Validate(ctx context.Context, clientID string) error {
	v.calls = append(v.calls, clientID)
	if len(v.errors) == 0 {
		return nil
	}
	e := v.errors[0]
	v.errors = v.errors[1:]
	return e
}
