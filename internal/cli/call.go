package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

// Extra apl-call-only exit codes per CALL-9.
const (
	ExitThrottled = 4
	ExitServer    = 5
)

type callOpts struct {
	body        string
	bodyFile    string
	headers     []string
	output      string
	contentType string
	statusOnly  bool
	scopes      []string
	timeout     time.Duration
}

var allowedMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

// throttledErr and serverErr yield CLIError with the new codes.
func throttledErr(msg string, args ...interface{}) error {
	return &CLIError{Code: ExitThrottled, Msg: fmt.Sprintf(msg, args...)}
}
func serverErr(msg string, args ...interface{}) error {
	return &CLIError{Code: ExitServer, Msg: fmt.Sprintf(msg, args...)}
}

// CallCmd returns the `apl call <handle> <METHOD> <url>` command.
//
// stdout: response body only (or empty with -o / --status-only).
// stderr: diagnostics only when stderr is a TTY, plus errors always.
func CallCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
	var o callOpts

	cmd := &cobra.Command{
		Use:   "call <handle> <METHOD> <url>",
		Short: "Issue an authenticated HTTP request via a stored handle",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCall(cmd.Context(), reg, st, stdout, stderr, args, &o)
		},
	}

	cmd.Flags().StringVar(&o.body, "body", "", "request body string")
	cmd.Flags().StringVar(&o.bodyFile, "body-file", "", "request body from file ('-' for stdin)")
	cmd.Flags().StringArrayVarP(&o.headers, "header", "H", nil, "extra header 'Key: Value' (repeatable)")
	cmd.Flags().StringVarP(&o.output, "output", "o", "", "write body to file instead of stdout")
	cmd.Flags().StringVar(&o.contentType, "content-type", "", "override Content-Type")
	cmd.Flags().BoolVar(&o.statusOnly, "status-only", false, "discard body; print status to stderr")
	cmd.Flags().StringArrayVar(&o.scopes, "scope", nil, "required scope (repeatable)")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 60*time.Second, "overall request timeout")
	return cmd
}

func runCall(parentCtx context.Context, reg *provider.Registry, st store.Store, stdout, stderr io.Writer, args []string, o *callOpts) error {
	// 1. Parse handle.
	h, err := ParseHandle(args[0])
	if err != nil {
		return userErr("apl call: %s (user error)", err.Error())
	}
	if err := ValidateProvider(h, reg); err != nil {
		return userErr("apl call: %s (user error)", err.Error())
	}

	// 2. Normalise + validate method.
	method := strings.ToUpper(args[1])
	if _, ok := allowedMethods[method]; !ok {
		return userErr("apl call: unsupported method %q (user error)", args[1])
	}

	// 3. Validate URL.
	u, uerr := url.Parse(args[2])
	if uerr != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return userErr("apl call: url must be absolute (user error)")
	}

	// 4. Header sanity + Authorization rejection.
	headers := make([][2]string, 0, len(o.headers))
	hasUserCT := false
	for _, raw := range o.headers {
		idx := strings.Index(raw, ":")
		if idx <= 0 {
			return userErr("apl call: malformed header %q (user error)", raw)
		}
		k := strings.TrimSpace(raw[:idx])
		v := strings.TrimPrefix(raw[idx+1:], " ")
		if strings.EqualFold(k, "Authorization") {
			return userErr("apl call: cannot override Authorization header (user error)")
		}
		if strings.EqualFold(k, "Content-Type") {
			if o.contentType != "" {
				return userErr("apl call: --content-type and -H 'Content-Type: ...' are mutually exclusive (user error)")
			}
			hasUserCT = true
		}
		headers = append(headers, [2]string{k, v})
	}

	// 5. Body flags mutex + materialise.
	if o.body != "" && o.bodyFile != "" {
		return userErr("apl call: --body and --body-file are mutually exclusive (user error)")
	}
	var bodyBytes []byte
	hasBody := false
	switch {
	case o.body != "":
		bodyBytes = []byte(o.body)
		hasBody = true
	case o.bodyFile == "-":
		b, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			return userErr("apl call: reading stdin: %s (user error)", rerr.Error())
		}
		bodyBytes = b
		hasBody = true
	case o.bodyFile != "":
		b, rerr := os.ReadFile(o.bodyFile)
		if rerr != nil {
			return userErr("apl call: reading body file: %s (user error)", rerr.Error())
		}
		bodyBytes = b
		hasBody = true
	}

	// 6. Resolve provider + load record.
	p, perr := reg.Resolve(h.Provider, h.Label)
	if perr != nil {
		if errors.Is(perr, provider.ErrNotConfigured) {
			return userErr("apl call: no OAuth client configured for %s. Run: apl setup %s --label %s (user error)", h.String(), h.Provider, h.Label)
		}
		return userErr("apl call: %s (user error)", perr.Error())
	}
	rec, gerr := st.Get(parentCtx, h.String())
	if gerr != nil {
		if errors.Is(gerr, store.ErrNotFound) {
			return authErr("apl call: no account for %s. Run: apl login %s (auth error)", h.String(), h.String())
		}
		return netErr("apl call: store get: %s (network error)", gerr.Error())
	}

	// 7. Scope guard pre-network.
	if missing := missingScopes(rec.Scopes, o.scopes); len(missing) > 0 {
		return authErr("apl call: missing scope(s): %s. Run: apl login %s --force --scope %s (auth error)",
			strings.Join(missing, ","), h.String(), strings.Join(missing, " --scope "))
	}

	// 8. Context with timeout covers refresh + HTTP.
	ctx, cancel := context.WithTimeout(parentCtx, o.timeout)
	defer cancel()

	// 9. Refresh before first request.
	tok, refreshed, rerr := p.Refresh(ctx, rec)
	if rerr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return netErr("apl call: timeout after %s (network error)", o.timeout.String())
		}
		return netErr("apl call: refresh: %s (network error)", rerr.Error())
	}
	if refreshed != nil {
		rec = refreshed
	}
	rec.AccessToken = tok
	_ = st.Put(parentCtx, rec)

	// 10. First attempt.
	resp, doErr := doRequest(ctx, method, args[2], rec.AccessToken, bodyBytes, hasBody, hasUserCT, o, headers)
	if doErr != nil {
		return mapNetErr(doErr, o.timeout)
	}

	// 11. On 401, refresh + retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		tok2, refreshed2, rerr2 := p.Refresh(ctx, rec)
		if rerr2 != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return netErr("apl call: timeout after %s (network error)", o.timeout.String())
			}
			return authErr("apl call: refresh after 401: %s (auth error)", rerr2.Error())
		}
		if refreshed2 != nil {
			rec = refreshed2
		}
		rec.AccessToken = tok2
		_ = st.Put(parentCtx, rec)

		resp, doErr = doRequest(ctx, method, args[2], rec.AccessToken, bodyBytes, hasBody, hasUserCT, o, headers)
		if doErr != nil {
			return mapNetErr(doErr, o.timeout)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// Stream body and exit 2.
			writeRespBody(resp, stdout, o)
			printStatusIfTTY(stderr, resp, o.statusOnly)
			return authErr("apl call: 401 after refresh (auth error)")
		}
	}

	defer resp.Body.Close()

	// 12. Print status to stderr (TTY or --status-only).
	printStatusIfTTY(stderr, resp, o.statusOnly)

	// 13. Stream body.
	writeRespBody(resp, stdout, o)

	// 14. Status → exit.
	return statusToExit(resp.StatusCode)
}

func doRequest(ctx context.Context, method, target, token string, bodyBytes []byte, hasBody, hasUserCT bool, o *callOpts, headers [][2]string) (*http.Response, error) {
	var body io.Reader
	if hasBody {
		body = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	// Auto Content-Type if body + no explicit override.
	if hasBody && o.contentType == "" && !hasUserCT {
		req.Header.Set("Content-Type", "application/json")
	}
	// User headers (may include Content-Type; multiple allowed).
	for _, kv := range headers {
		req.Header.Add(kv[0], kv[1])
	}
	// --content-type wins.
	if o.contentType != "" {
		req.Header.Set("Content-Type", o.contentType)
	}

	client := &http.Client{Timeout: 0}
	return client.Do(req)
}

func mapNetErr(err error, timeout time.Duration) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline") || strings.Contains(lower, "timeout") {
		return netErr("apl call: timeout after %s (network error)", timeout.String())
	}
	return netErr("apl call: %s (network error)", msg)
}

func writeRespBody(resp *http.Response, stdout io.Writer, o *callOpts) {
	if o.statusOnly {
		_, _ = io.Copy(io.Discard, resp.Body)
		return
	}
	var dest io.Writer = stdout
	if o.output != "" && o.output != "-" {
		f, err := os.Create(o.output)
		if err != nil {
			// Still drain; caller will see error via exit mapping. Best-effort.
			_, _ = io.Copy(io.Discard, resp.Body)
			return
		}
		defer f.Close()
		dest = f
	}
	_, _ = io.Copy(dest, resp.Body)
}

func printStatusIfTTY(stderr io.Writer, resp *http.Response, statusOnly bool) {
	if !statusOnly && !isStderrTTY(stderr) {
		return
	}
	fmt.Fprintf(stderr, "HTTP/%d.%d %d %s\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, http.StatusText(resp.StatusCode))
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(stderr, "Content-Type: %s\n", ct)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); wa != "" {
		fmt.Fprintf(stderr, "WWW-Authenticate: %s\n", wa)
	}
}

// isStderrTTY returns true if the given writer is os.Stderr AND that fd is a
// terminal. Any other writer (bytes.Buffer, pipe) is treated as non-TTY.
func isStderrTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func statusToExit(code int) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == 429:
		return throttledErr("apl call: throttled (throttled)")
	case code >= 500:
		return serverErr("apl call: server error %d (server error)", code)
	case code >= 400:
		return userErr("apl call: HTTP %d (user error)", code)
	default:
		// 3xx shouldn't appear (net/http follows redirects by default).
		return userErr("apl call: unexpected status %d (user error)", code)
	}
}

func missingScopes(granted, required []string) []string {
	if len(required) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		set[s] = struct{}{}
	}
	var miss []string
	for _, r := range required {
		if _, ok := set[r]; !ok {
			miss = append(miss, r)
		}
	}
	return miss
}
