package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestTokenRecordHandle(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		label    string
		want     string
	}{
		{"google-work", "google", "work", "google:work"},
		{"microsoft-volentis", "microsoft", "volentis", "microsoft:volentis"},
		{"empty-label", "google", "", "google:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &TokenRecord{Provider: tc.provider, Label: tc.label}
			if got := rec.HandleString(); got != tc.want {
				t.Fatalf("HandleString() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestTokenRecordJSONRoundTrip(t *testing.T) {
	expires := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	issued := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	rec := &TokenRecord{
		Provider:     "google",
		Label:        "work",
		Handle:       "google:work",
		Subject:      "muthu@example.com",
		Tenant:       "",
		RefreshToken: "rt-xyz",
		AccessToken:  "at-xyz",
		ExpiresAt:    expires,
		Scopes:       []string{"openid", "https://www.googleapis.com/auth/userinfo.email"},
		IssuedAt:     issued,
	}

	blob, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Tenant omitempty — ensure absent for empty tenant.
	var asMap map[string]any
	if err := json.Unmarshal(blob, &asMap); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if _, ok := asMap["tenant"]; ok {
		t.Errorf("tenant should be omitted when empty; got %v", asMap["tenant"])
	}
	for _, k := range []string{"provider", "label", "handle", "sub", "refresh_token", "access_token", "expires_at", "scopes", "issued_at"} {
		if _, ok := asMap[k]; !ok {
			t.Errorf("missing json field %q in %s", k, string(blob))
		}
	}

	var got TokenRecord
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(&got, rec) {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", got, *rec)
	}
}

// storeContract exercises the Store interface contract. Any backend
// implementation should pass this. newStore must return a fresh, empty store.
func storeContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()

	t.Run("STORE-6_put_get_roundtrip", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		rec := &TokenRecord{
			Provider:     "google",
			Label:        "work",
			Subject:      "a@b.com",
			RefreshToken: "rt",
			AccessToken:  "at",
			ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
			Scopes:       []string{"openid"},
			IssuedAt:     time.Now().UTC().Truncate(time.Second),
		}
		if err := s.Put(ctx, rec); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.Get(ctx, "google:work")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Handle != "google:work" {
			t.Errorf("handle not populated on put; got %q", got.Handle)
		}
		if got.RefreshToken != "rt" || got.AccessToken != "at" {
			t.Errorf("tokens mismatch: %+v", got)
		}
	})

	t.Run("STORE-12_get_missing_returns_ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		_, err := s.Get(context.Background(), "google:nope")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound; got %v", err)
		}
	})

	t.Run("STORE-8_list_empty_on_fresh_store", func(t *testing.T) {
		s := newStore(t)
		recs, err := s.List(context.Background())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(recs) != 0 {
			t.Fatalf("expected empty; got %d", len(recs))
		}
	})

	t.Run("STORE-8_list_returns_all_handles", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		handles := []string{"google:work", "google:personal", "microsoft:volentis"}
		for _, h := range handles {
			rec := &TokenRecord{
				Provider:     splitProvider(h),
				Label:        splitLabel(h),
				RefreshToken: "rt-" + h,
				ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
				Scopes:       []string{"openid"},
			}
			if err := s.Put(ctx, rec); err != nil {
				t.Fatalf("put %s: %v", h, err)
			}
		}
		recs, err := s.List(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var got []string
		for _, r := range recs {
			got = append(got, r.Handle)
		}
		sort.Strings(got)
		want := append([]string(nil), handles...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("list mismatch:\n got=%v\nwant=%v", got, want)
		}
		// Ensure __index is not in the list.
		for _, h := range got {
			if h == "__index" {
				t.Fatal("__index should not appear in List() result")
			}
		}
	})

	t.Run("STORE-10_delete_wipes", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		rec := &TokenRecord{
			Provider:     "google",
			Label:        "work",
			RefreshToken: "rt",
			ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
		}
		if err := s.Put(ctx, rec); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := s.Delete(ctx, "google:work"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.Get(ctx, "google:work"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound after delete; got %v", err)
		}
		recs, err := s.List(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(recs) != 0 {
			t.Fatalf("expected empty after delete; got %d", len(recs))
		}
	})
}

func splitProvider(handle string) string {
	for i := 0; i < len(handle); i++ {
		if handle[i] == ':' {
			return handle[:i]
		}
	}
	return handle
}

func splitLabel(handle string) string {
	for i := 0; i < len(handle); i++ {
		if handle[i] == ':' {
			return handle[i+1:]
		}
	}
	return ""
}
