package provider

import (
	"net/url"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

func TestMicrosoft_ExpandScopes(t *testing.T) {
	m := NewMicrosoft(config.ProviderConfig{ClientID: "x"})
	got, err := m.ExpandScopes([]string{"Mail.Read", "User.Read"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Mail.Read", "User.Read"}
	if !strSliceEq(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}

	if _, err := m.ExpandScopes([]string{"nope"}); err == nil {
		t.Errorf("expected error")
	}

	got, err = m.ExpandScopes([]string{"https://graph.microsoft.com/Mail.Read"})
	if err != nil || got[0] != "https://graph.microsoft.com/Mail.Read" {
		t.Errorf("URI passthrough failed: %v", got)
	}
}

func TestMicrosoft_AuthURL_DefaultTenantAndOfflineAccess(t *testing.T) {
	m := NewMicrosoft(config.ProviderConfig{ClientID: "mid"})
	scopes := m.normalizeScopes([]string{"Mail.Read"})
	u, err := m.buildAuthURL("common", "http://127.0.0.1:5555/callback", "S", "C", scopes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	if !strings.Contains(parsed.Path, "/common/oauth2/v2.0/authorize") {
		t.Errorf("tenant path wrong: %q", parsed.Path)
	}
	sc := parsed.Query().Get("scope")
	if !strings.Contains(sc, "offline_access") {
		t.Errorf("offline_access not auto-injected: %q", sc)
	}
	if !strings.Contains(sc, "Mail.Read") {
		t.Errorf("Mail.Read missing: %q", sc)
	}
}

func TestMicrosoft_AuthURL_CustomTenant(t *testing.T) {
	m := NewMicrosoft(config.ProviderConfig{ClientID: "mid"})
	scopes := m.normalizeScopes([]string{"User.Read"})
	u, err := m.buildAuthURL("contoso.onmicrosoft.com", "http://127.0.0.1:1/callback", "S", "C", scopes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	if !strings.HasPrefix(parsed.Path, "/contoso.onmicrosoft.com/") {
		t.Errorf("tenant path wrong: %q", parsed.Path)
	}
}

func TestMicrosoft_NormalizeScopes_NoDupe(t *testing.T) {
	m := NewMicrosoft(config.ProviderConfig{ClientID: "mid"})
	got := m.normalizeScopes([]string{"offline_access", "User.Read"})
	count := 0
	for _, s := range got {
		if s == "offline_access" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("offline_access duplicated: %v", got)
	}
}

func TestMicrosoft_Logout_NoOp(t *testing.T) {
	m := NewMicrosoft(config.ProviderConfig{ClientID: "mid"})
	if err := m.Logout(nil, nil); err != nil {
		t.Errorf("Logout should be a no-op, got %v", err)
	}
}
