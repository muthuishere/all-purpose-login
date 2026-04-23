package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// successHTML is shown to the user's browser after a successful callback.
// Constant — it never echoes code, state, or any query parameter.
const successHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>Signed in</title></head>
<body><h1>Signed in. You can close this tab.</h1></body></html>`

type callbackResult struct {
	code  string
	state string
	err   error
}

// LoopbackServer is a single-shot HTTP server bound to 127.0.0.1 that
// receives the OAuth authorization_code callback.
type LoopbackServer struct {
	listener net.Listener
	server   *http.Server
	url      string

	resultCh chan callbackResult
	once     sync.Once
	closed   chan struct{}
}

// NewLoopbackServer binds 127.0.0.1:0 and serves the OAuth callback at root ("/").
// The advertised redirect URI uses the hostname `localhost` because Microsoft
// Identity Platform requires an exact hostname match and does not accept
// `127.0.0.1` for loopback redirects (Google accepts either). The browser
// resolves `localhost` back to 127.0.0.1 via /etc/hosts, so the listener still
// receives the callback. The path is root ("/") because Microsoft requires the
// runtime redirect_uri path to match the registered path exactly, and we
// register `http://localhost` (no path) during `apl setup ms`.
func NewLoopbackServer(ctx context.Context) (*LoopbackServer, error) {
	// Bind 127.0.0.1 literally — never 0.0.0.0 (must be unreachable from the network).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback: %w", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ls := &LoopbackServer{
		listener: ln,
		url:      fmt.Sprintf("http://localhost:%d", addr.Port),
		resultCh: make(chan callbackResult, 1),
		closed:   make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", ls.handleCallback)
	ls.server = &http.Server{Handler: mux}

	go func() {
		_ = ls.server.Serve(ln)
	}()

	// Tie lifetime to ctx.
	go func() {
		select {
		case <-ctx.Done():
			ls.Close()
		case <-ls.closed:
		}
	}()

	return ls, nil
}

func (s *LoopbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	oauthErr := q.Get("error")

	// Ignore requests with neither a code nor an error (favicon.ico, probes).
	if code == "" && oauthErr == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(successHTML))

	var res callbackResult
	switch {
	case oauthErr != "":
		res = callbackResult{err: fmt.Errorf("oauth error: %s", oauthErr)}
	default:
		res = callbackResult{code: code, state: state}
	}

	select {
	case s.resultCh <- res:
	default:
		// Already delivered — drop subsequent callbacks.
	}
}

// URL returns the base URL of this server (no trailing slash).
func (s *LoopbackServer) URL() string { return s.url }

// RedirectURI returns the callback URL to send as the `redirect_uri` parameter.
// Uses root path ("/") for Microsoft/Google loopback compatibility.
func (s *LoopbackServer) RedirectURI() string { return s.url + "/" }

// Wait blocks until a callback arrives, ctx is done, or the server closes.
// It validates state against expectedState using a constant-time compare.
func (s *LoopbackServer) Wait(ctx context.Context, expectedState string) (code, state string, err error) {
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case <-s.closed:
		return "", "", errors.New("loopback server closed")
	case res := <-s.resultCh:
		if res.err != nil {
			return "", "", res.err
		}
		if !ConstantTimeEqual(res.state, expectedState) {
			return "", "", errors.New("state mismatch (possible CSRF)")
		}
		return res.code, res.state, nil
	}
}

// Close shuts the server down. Safe to call multiple times.
func (s *LoopbackServer) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closed)
		err = s.server.Shutdown(context.Background())
	})
	return err
}
