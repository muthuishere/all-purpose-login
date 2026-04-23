package oauth

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLoopbackServer_BindsLoopbackAndReturnsURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := NewLoopbackServer(ctx)
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer srv.Close()

	u, err := url.Parse(srv.URL())
	if err != nil {
		t.Fatalf("parse URL %q: %v", srv.URL(), err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	// The advertised URL uses hostname "localhost" for Microsoft compatibility
	// (AADSTS50011 rejects 127.0.0.1). The underlying listener still binds
	// 127.0.0.1 — verified via the listener's resolved TCPAddr below.
	if host != "localhost" {
		t.Fatalf("host = %q, want localhost", host)
	}
	if port == "0" || port == "" {
		t.Fatalf("port = %q, want non-zero", port)
	}
	if !strings.HasPrefix(srv.URL(), "http://localhost:") {
		t.Fatalf("URL %q does not start with http://localhost:", srv.URL())
	}
	// The physical listener must still bind 127.0.0.1 (not 0.0.0.0).
	lnAddr := srv.listener.Addr().String()
	if !strings.HasPrefix(lnAddr, "127.0.0.1:") {
		t.Fatalf("listener addr = %q, want 127.0.0.1:<port>", lnAddr)
	}
}

func TestLoopbackServer_DeliversCodeAndState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv, err := NewLoopbackServer(ctx)
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer srv.Close()

	expectedState := "the-state"
	go func() {
		// Give the server a beat to be ready; it is ready on construction.
		resp, err := http.Get(srv.URL() + "/callback?code=CODE123&state=" + expectedState)
		if err == nil {
			resp.Body.Close()
		}
	}()

	code, state, err := srv.Wait(ctx, expectedState)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != "CODE123" {
		t.Fatalf("code = %q, want CODE123", code)
	}
	if state != expectedState {
		t.Fatalf("state = %q, want %q", state, expectedState)
	}
}

func TestLoopbackServer_StateMismatchYieldsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv, err := NewLoopbackServer(ctx)
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer srv.Close()

	go func() {
		resp, err := http.Get(srv.URL() + "/callback?code=CODE&state=WRONG")
		if err == nil {
			resp.Body.Close()
		}
	}()

	_, _, err = srv.Wait(ctx, "EXPECTED")
	if err == nil {
		t.Fatal("expected error on state mismatch, got nil")
	}
}

func TestLoopbackServer_ContextCancelReturnsErr(t *testing.T) {
	parent := context.Background()
	srv, err := NewLoopbackServer(parent)
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(parent)
	cancel() // already cancelled

	_, _, err = srv.Wait(ctx, "anything")
	if err == nil {
		t.Fatal("expected error on cancelled ctx")
	}
}
