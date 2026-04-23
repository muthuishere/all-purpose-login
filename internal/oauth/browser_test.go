package oauth

import (
	"errors"
	"strings"
	"testing"
)

func TestOpen_DelegatesToOpener(t *testing.T) {
	orig := Opener
	defer func() { Opener = orig }()

	var got string
	Opener = func(u string) error {
		got = u
		return nil
	}
	if err := Open("http://example.com/auth"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "http://example.com/auth" {
		t.Fatalf("opener received %q", got)
	}
}

func TestOpen_WrapsOpenerError(t *testing.T) {
	orig := Opener
	defer func() { Opener = orig }()

	Opener = func(u string) error { return errors.New("no display") }
	err := Open("http://example.com/auth")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no display") {
		t.Fatalf("error %q does not wrap opener err", err.Error())
	}
}
