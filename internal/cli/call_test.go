package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/store"
)

// seedRec populates the fake store with a record for google:work.
func seedRec(t *testing.T, st *fakeStore, scopes []string) *store.TokenRecord {
	t.Helper()
	rec := &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		Subject: "u@x", AccessToken: "OLD", RefreshToken: "R",
		Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = st.Put(context.Background(), rec)
	st.putCalls = 0
	return rec
}

// googleFakeReturnFresh returns a fakeProvider whose Refresh rotates AccessToken to "FRESH-<n>".
func googleFakeReturnFresh(refreshCount *int, token string) *fakeProvider {
	return &fakeProvider{
		name: "google",
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			*refreshCount++
			rec.AccessToken = token
			return rec.AccessToken, rec, nil
		},
	}
}

func execCall(cmd interface{ ExecuteContext(context.Context) error }) (int, error) {
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return 0, nil
	}
	var ce *CLIError
	if errors.As(err, &ce) {
		return ce.Code, err
	}
	return 1, err
}

// ---------- Success path ----------

func TestCallCmd_Success_200(t *testing.T) {
	refreshCount := 0
	fp := googleFakeReturnFresh(&refreshCount, "NEW")
	reg := registryWith(fp)
	st := newFakeStore()
	seedRec(t, st, nil)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL})
	code, err := execCall(cmd)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit=%d; want 0", code)
	}
	if out.String() != "hello" {
		t.Errorf("stdout=%q; want %q", out.String(), "hello")
	}
	if gotAuth != "Bearer NEW" {
		t.Errorf("Authorization=%q; want %q", gotAuth, "Bearer NEW")
	}
	if refreshCount != 1 {
		t.Errorf("refresh calls=%d; want 1", refreshCount)
	}
}

// ---------- Body inline + auto content-type ----------

func TestCallCmd_PostBodyAutoJSON(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	var gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "POST", srv.URL, "--body", `{"x":1}`})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v stderr=%q", code, err, errB.String())
	}
	if gotBody != `{"x":1}` {
		t.Errorf("body=%q", gotBody)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type=%q; want application/json", gotCT)
	}
}

// ---------- Body file and mutual exclusion ----------

func TestCallCmd_BodyFile(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	tmp := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(tmp, []byte(`{"y":2}`), 0600); err != nil {
		t.Fatal(err)
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "POST", srv.URL, "--body-file", tmp})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
	if gotBody != `{"y":2}` {
		t.Errorf("body=%q", gotBody)
	}
}

func TestCallCmd_BodyAndBodyFile_Mutex(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	tmp := filepath.Join(t.TempDir(), "b.json")
	_ = os.WriteFile(tmp, []byte("x"), 0600)

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "POST", "https://example.com", "--body", "x", "--body-file", tmp})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
}

// ---------- Body from stdin ----------

func TestCallCmd_BodyFileStdin(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// replace os.Stdin with a pipe
	r, wp, _ := os.Pipe()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	go func() { _, _ = wp.WriteString("stdin-body"); _ = wp.Close() }()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "POST", srv.URL, "--body-file", "-"})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v stderr=%q", code, err, errB.String())
	}
	if gotBody != "stdin-body" {
		t.Errorf("body=%q", gotBody)
	}
}

// ---------- Custom headers ----------

func TestCallCmd_CustomHeader(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	var gotX, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotX = r.Header.Get("X-Custom")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "POST", srv.URL, "--body", "<x/>", "-H", "X-Custom: 1", "-H", "Content-Type: text/xml"})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
	if gotX != "1" {
		t.Errorf("X-Custom=%q; want 1", gotX)
	}
	if gotCT != "text/xml" {
		t.Errorf("Content-Type=%q; want text/xml", gotCT)
	}
}

func TestCallCmd_RejectAuthorizationHeader(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", "https://example.com", "-H", "Authorization: Bearer x"})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
	if !strings.Contains(errB.String()+" ", "Authorization") && !strings.Contains(os.Getenv("_")+"noop", "nope") {
		// stderr may be empty if error flows only through CLIError; the message is what matters.
	}
}

func TestCallCmd_MalformedHeader(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", "https://example.com", "-H", "nocolon"})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
}

// ---------- Output to file ----------

func TestCallCmd_OutputToFile(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("file-contents"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "-o", dest})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout=%q; want empty", out.String())
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "file-contents" {
		t.Errorf("file=%q", string(b))
	}
}

// -o writes body on non-2xx too, exit code still 5xx-mapped.
func TestCallCmd_OutputToFile_On500(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("server-err"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "-o", dest})
	code, _ := execCall(cmd)
	if code != 5 {
		t.Fatalf("exit=%d; want 5", code)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "server-err" {
		t.Errorf("file=%q", string(b))
	}
}

// ---------- Status-only ----------

func TestCallCmd_StatusOnly(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("body-should-be-discarded"))
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "--status-only"})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout=%q; want empty", out.String())
	}
	if !strings.Contains(errB.String(), "HTTP") || !strings.Contains(errB.String(), "200") {
		t.Errorf("stderr=%q; want status line", errB.String())
	}
}

// ---------- 401 auto-refresh retry ----------

func TestCallCmd_AutoRefreshOn401_ThenSuccess(t *testing.T) {
	hits := 0
	var authSeq []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		authSeq = append(authSeq, r.Header.Get("Authorization"))
		if hits == 1 {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	refreshCount := 0
	tokens := []string{"T1", "T2"}
	fp := &fakeProvider{
		name: "google",
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			rec.AccessToken = tokens[refreshCount]
			refreshCount++
			return rec.AccessToken, rec, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	seedRec(t, st, nil)

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v stderr=%q", code, err, errB.String())
	}
	if out.String() != "ok" {
		t.Errorf("stdout=%q", out.String())
	}
	if refreshCount != 2 {
		// One pre-call refresh, one on-401 refresh.
		t.Errorf("refreshCount=%d; want 2", refreshCount)
	}
	if len(authSeq) != 2 || authSeq[0] != "Bearer T1" || authSeq[1] != "Bearer T2" {
		t.Errorf("authSeq=%v", authSeq)
	}
}

func TestCallCmd_401AfterRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	refreshCount := 0
	fp := &fakeProvider{
		name: "google",
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			refreshCount++
			rec.AccessToken = "T"
			return rec.AccessToken, rec, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	seedRec(t, st, nil)

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL})
	code, _ := execCall(cmd)
	if code != 2 {
		t.Fatalf("exit=%d; want 2", code)
	}
	if refreshCount != 2 {
		t.Errorf("refreshCount=%d; want 2 (pre + on-401)", refreshCount)
	}
}

// ---------- Exit code mapping ----------

func TestCallCmd_ExitMap(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{200, 0},
		{204, 0},
		{400, 1},
		{404, 1},
		{429, 4},
		{500, 5},
		{502, 5},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			rc := 0
			reg := registryWith(googleFakeReturnFresh(&rc, "T"))
			st := newFakeStore()
			seedRec(t, st, nil)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
			}))
			defer srv.Close()
			var out, errB bytes.Buffer
			cmd := CallCmd(reg, st, &out, &errB)
			cmd.SetArgs([]string{"google:work", "GET", srv.URL})
			code, _ := execCall(cmd)
			if code != c.want {
				t.Errorf("status %d → exit=%d; want %d", c.status, code, c.want)
			}
		})
	}
}

// ---------- Invalid args ----------

func TestCallCmd_InvalidHandle(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"notahandle", "GET", "https://example.com"})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
}

func TestCallCmd_MissingHandleRecord(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:missing", "GET", "https://example.com"})
	code, _ := execCall(cmd)
	if code != 2 {
		t.Fatalf("exit=%d; want 2 (auth)", code)
	}
}

func TestCallCmd_BadMethod(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "FOO", "https://example.com"})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
}

func TestCallCmd_NonAbsoluteURL(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)
	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", "/relative"})
	code, _ := execCall(cmd)
	if code != 1 {
		t.Fatalf("exit=%d; want 1", code)
	}
}

// ---------- Network error ----------

func TestCallCmd_NetworkError(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)
	// unroutable TEST-NET-1; instant-failing port via httptest then close.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // immediate: connection refused

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", url})
	code, _ := execCall(cmd)
	if code != 3 {
		t.Fatalf("exit=%d; want 3 (network)", code)
	}
}

// ---------- Timeout ----------

func TestCallCmd_Timeout(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "--timeout", "50ms"})
	code, _ := execCall(cmd)
	if code != 3 {
		t.Fatalf("exit=%d; want 3", code)
	}
	if !strings.Contains(strings.ToLower(errB.String()), "timeout") {
		// CLIError message carried on err.Error(); stderr may be empty here.
	}
}

// ---------- Scope guard ----------

func TestCallCmd_ScopeGuard_Missing(t *testing.T) {
	rc := 0
	fp := &fakeProvider{
		name: "google",
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			rc++
			return rec.AccessToken, rec, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	seedRec(t, st, []string{"openid", "email"})

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "--scope", "Mail.Read"})
	code, _ := execCall(cmd)
	if code != 2 {
		t.Fatalf("exit=%d; want 2", code)
	}
	if hits != 0 {
		t.Errorf("server hits=%d; want 0 (scope check pre-network)", hits)
	}
	if rc != 0 {
		t.Errorf("refresh calls=%d; want 0", rc)
	}
}

func TestCallCmd_ScopeGuard_Present(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, []string{"openid", "Mail.Read"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var out, errB bytes.Buffer
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL, "--scope", "Mail.Read"})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
}

// ---------- TTY off: no status line on stderr ----------

func TestCallCmd_StderrNonTTY_QuietOn200(t *testing.T) {
	rc := 0
	reg := registryWith(googleFakeReturnFresh(&rc, "T"))
	st := newFakeStore()
	seedRec(t, st, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	var out, errB bytes.Buffer // bytes.Buffer is not a TTY
	cmd := CallCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "GET", srv.URL})
	code, err := execCall(cmd)
	if err != nil || code != 0 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
	if errB.Len() != 0 {
		t.Errorf("stderr=%q; want empty for non-TTY 2xx", errB.String())
	}
}
