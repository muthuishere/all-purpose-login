package cli

import "fmt"

// Exit codes per spec-cli-command-surface CLI-3.
const (
	ExitOK      = 0
	ExitUser    = 1
	ExitAuth    = 2
	ExitNetwork = 3
)

// CLIError carries a user-facing stderr message and an exit code.
// cobra's RunE returns one of these; main maps it to os.Exit.
type CLIError struct {
	Code int
	Msg  string
}

func (e *CLIError) Error() string { return e.Msg }

func userErr(msg string, args ...interface{}) error {
	return &CLIError{Code: ExitUser, Msg: fmt.Sprintf(msg, args...)}
}

func authErr(msg string, args ...interface{}) error {
	return &CLIError{Code: ExitAuth, Msg: fmt.Sprintf(msg, args...)}
}

func netErr(msg string, args ...interface{}) error {
	return &CLIError{Code: ExitNetwork, Msg: fmt.Sprintf(msg, args...)}
}
