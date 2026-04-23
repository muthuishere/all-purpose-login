package oauth

import (
	"encoding/hex"
	"testing"
)

func TestGenerateState_Is32HexBytes(t *testing.T) {
	s, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if len(s) != 64 {
		t.Fatalf("state length = %d, want 64", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded bytes = %d, want 32", len(raw))
	}
}

func TestConstantTimeEqual_Equal(t *testing.T) {
	if !ConstantTimeEqual("abc123", "abc123") {
		t.Fatal("expected true for equal strings")
	}
}

func TestConstantTimeEqual_UnequalSameLength(t *testing.T) {
	if ConstantTimeEqual("abc123", "abc124") {
		t.Fatal("expected false for unequal same-length strings")
	}
}

func TestConstantTimeEqual_DifferentLength(t *testing.T) {
	// Must not panic; must return false.
	if ConstantTimeEqual("abc", "abcdef") {
		t.Fatal("expected false for different-length inputs")
	}
	if ConstantTimeEqual("", "x") {
		t.Fatal("expected false for empty vs non-empty")
	}
}
