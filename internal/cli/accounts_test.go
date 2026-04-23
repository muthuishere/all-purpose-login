package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/store"
)

func TestAccountsCmd_TableNoTenant(t *testing.T) {
	reg := registryWith(&fakeProvider{name: "google"})
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work", Subject: "u@a",
	}
	var out, errB bytes.Buffer
	cmd := AccountsCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	s := out.String()
	for _, hdr := range []string{"PROVIDER", "LABEL", "EMAIL", "STORED"} {
		if !strings.Contains(s, hdr) {
			t.Errorf("missing header %s in %q", hdr, s)
		}
	}
	if strings.Contains(s, "TENANT") {
		t.Errorf("TENANT column should be omitted, got %q", s)
	}
}

func TestAccountsCmd_TableWithTenant(t *testing.T) {
	reg := registryWith(&fakeProvider{name: "google"}, &fakeProvider{name: "ms"})
	st := newFakeStore()
	st.records["ms:volentis"] = &store.TokenRecord{
		Provider: "ms", Label: "volentis", Handle: "ms:volentis",
		Subject: "u@v", Tenant: "volentis.onmicrosoft.com",
	}
	var out, errB bytes.Buffer
	cmd := AccountsCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(out.String(), "TENANT") {
		t.Errorf("TENANT header missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "volentis.onmicrosoft.com") {
		t.Errorf("tenant value missing: %q", out.String())
	}
}

func TestAccountsCmd_JSON(t *testing.T) {
	reg := registryWith(&fakeProvider{name: "google"})
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work", Subject: "u@a",
		Scopes: []string{"s1"}, ExpiresAt: time.Unix(1000, 0),
	}
	var out, errB bytes.Buffer
	cmd := AccountsCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	var parsed []map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v; raw=%q", err, out.String())
	}
	if len(parsed) != 1 {
		t.Fatalf("want 1 entry, got %d", len(parsed))
	}
	for _, key := range []string{"provider", "label", "handle", "email", "tenant", "stored", "scopes", "expires_at"} {
		if _, ok := parsed[0][key]; !ok {
			t.Errorf("missing key %q in %v", key, parsed[0])
		}
	}
	if parsed[0]["handle"] != "google:work" {
		t.Errorf("handle = %v", parsed[0]["handle"])
	}
	if parsed[0]["tenant"] != nil {
		t.Errorf("tenant should be null, got %v", parsed[0]["tenant"])
	}
}

func TestAccountsCmd_EmptyList(t *testing.T) {
	reg := registryWith(&fakeProvider{name: "google"})
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := AccountsCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(out.String(), "No accounts") {
		t.Errorf("stdout = %q", out.String())
	}
}
