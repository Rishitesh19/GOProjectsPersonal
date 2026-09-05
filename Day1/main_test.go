package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T, approve approvalFunc) (*receiver, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	s := newReceiver(root, "test-code", 100, approve, context.Background())
	s.hosts = map[string]bool{"mac:8080": true}
	return s, dir
}
func call(s *receiver, path, body, ticket string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://mac:8080"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://mac:8080")
	r.Header.Set("Authorization", "Bearer test-code")
	r.Header.Set("X-Upload-Ticket", ticket)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func ticket(t *testing.T, s *receiver, name string, size int) string {
	t.Helper()
	body, _ := json.Marshal(offer{Name: name, Size: int64(size)})
	w := call(s, "/request", string(body), "")
	if w.Code != 200 {
		t.Fatalf("approval: %d %s", w.Code, w.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response["ticket"]
}
func yes(context.Context, string, int64, string) bool { return true }
func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected saved files: %v %v", entries, err)
	}
}

func TestConsentAndPrivacy(t *testing.T) {
	calls := 0
	s, dir := setup(t, func(ctx context.Context, name string, size int64, peer string) bool {
		calls++
		return name == "photo.jpg" && size == 5
	})
	if w := call(s, "/upload", "hello", ""); w.Code != 403 {
		t.Fatal("upload without approval allowed")
	}
	assertEmpty(t, dir)
	permit := ticket(t, s, "photo.jpg", 5)
	assertEmpty(t, dir) // Even approval does not save any file contents.
	if w := call(s, "/upload", "hello", permit); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if w := call(s, "/upload", "hello", permit); w.Code != 403 {
		t.Fatal("ticket replay allowed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || calls != 1 {
		t.Fatal("unexpected saves or approvals")
	}
	content, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || string(content) != "hello" {
		t.Fatal("file content mismatch")
	}
	for _, path := range []string{"/", "/" + entries[0].Name()} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "http://mac:8080"+path, nil))
		if strings.Contains(w.Body.String(), entries[0].Name()) || strings.Contains(w.Body.String(), "hello") {
			t.Fatal("file exposed")
		}
		if path != "/" && w.Code != 404 {
			t.Fatal("download route exists")
		}
	}
}

func TestDeclinedAndCanceled(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		s, dir := setup(t, func(context.Context, string, int64, string) bool { return cancelled })
		if cancelled {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			s.ctx = ctx
		}
		w := call(s, "/request", `{"name":"a","size":1}`, "")
		if w.Code == 200 {
			t.Fatal("denied or ended session accepted")
		}
		if w = call(s, "/upload", "a", "fake"); w.Code == 200 {
			t.Fatal("unapproved upload accepted")
		}
		assertEmpty(t, dir)
	}
}

func TestSizeQuotaAndOverwrite(t *testing.T) {
	s, dir := setup(t, yes)
	s.quota = 5
	first := ticket(t, s, "a.txt", 3)
	if w := call(s, "/upload", "too long", first); w.Code != 400 {
		t.Fatal("size mismatch accepted")
	}
	assertEmpty(t, dir)
	for i := 0; i < 2; i++ {
		p := ticket(t, s, "a.txt", 2)
		if w := call(s, "/upload", "ok", p); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("duplicate filename overwritten")
	}
	if w := call(s, "/request", `{"name":"b","size":2}`, ""); w.Code != 413 {
		t.Fatal("quota not enforced")
	}
	s.count = 20
	if w := call(s, "/request", `{"name":"empty","size":0}`, ""); w.Code != 413 {
		t.Fatal("file count quota not enforced")
	}
}

func TestUnknownLengthMismatchRemovesPartial(t *testing.T) {
	s, dir := setup(t, yes)
	p := ticket(t, s, "a", 2)
	r := httptest.NewRequest("POST", "http://mac:8080/upload", strings.NewReader("too long"))
	r.ContentLength = -1
	r.Header.Set("Origin", "http://mac:8080")
	r.Header.Set("Authorization", "Bearer test-code")
	r.Header.Set("X-Upload-Ticket", p)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("oversized stream accepted")
	}
	assertEmpty(t, dir)
	if s.used != 0 {
		t.Fatal("failed upload consumed quota")
	}
}

func TestExpiredAndConcurrentApproval(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s, _ := setup(t, func(context.Context, string, int64, string) bool { close(entered); <-release; return true })
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- call(s, "/request", `{"name":"a","size":1}`, "") }()
	<-entered
	if w := call(s, "/request", `{"name":"b","size":1}`, ""); w.Code != 409 {
		t.Fatal("concurrent approval allowed")
	}
	close(release)
	<-done
	if w := call(s, "/request", `{"name":"b","size":1}`, ""); w.Code != 409 {
		t.Fatal("pending transfer did not reserve slot")
	}
	s.mu.Lock()
	p := s.pending.ticket
	s.pending.expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if w := call(s, "/upload", "a", p); w.Code != 403 {
		t.Fatal("expired ticket allowed")
	}
	s.approve = yes
	ticket(t, s, "new", 1)
}

func TestOriginHostAndAuthentication(t *testing.T) {
	for _, tc := range []struct{ host, origin, auth, site string }{
		{"mac:8080", "http://evil.example", "Bearer test-code", ""},
		{"evil.example", "http://evil.example", "Bearer test-code", ""},
		{"mac:8080", "", "Bearer test-code", ""},
		{"mac:8080", "http://mac:8080", "Bearer wrong", ""},
		{"mac:8080", "http://mac:8080", "Bearer test-code", "cross-site"},
	} {
		s, dir := setup(t, yes)
		r := httptest.NewRequest("POST", "http://"+tc.host+"/request", strings.NewReader(`{"name":"a","size":1}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("untrusted request accepted")
		}
		assertEmpty(t, dir)
	}
	s, _ := setup(t, yes)
	for i := 0; i < 5; i++ {
		r := httptest.NewRequest("POST", "http://mac:8080/request", nil)
		r.Header.Set("Origin", "http://mac:8080")
		s.ServeHTTP(httptest.NewRecorder(), r)
	}
	if w := call(s, "/request", `{"name":"a","size":1}`, ""); w.Code != 429 {
		t.Fatal("lockout missing")
	}
	s.lockedUntil = time.Now().Add(-time.Second)
	ticket(t, s, "a", 1)
}

func TestInvalidOffersAndOnce(t *testing.T) {
	s, dir := setup(t, yes)
	for _, body := range []string{`{"name":"../x","size":1}`, `{"name":"x","size":-1}`, `{"name":"x","size":52428801}`, `{"name":"x","size":1} {}`, `{"name":"x","size":1,"other":true}`, strings.Repeat("x", 5000)} {
		if w := call(s, "/request", body, ""); w.Code != 400 {
			t.Fatalf("invalid offer accepted: %s", body)
		}
	}
	assertEmpty(t, dir)
	stopped := false
	s.onSuccess = func() { stopped = true }
	p := ticket(t, s, "a", 1)
	call(s, "/upload", "a", p)
	if !stopped {
		t.Fatal("once callback not called")
	}
}

func TestApprovalBoundToSender(t *testing.T) {
	s, dir := setup(t, yes)
	p := ticket(t, s, "a", 1)
	r := httptest.NewRequest("POST", "http://mac:8080/upload", strings.NewReader("a"))
	r.RemoteAddr = "192.0.2.99:5000"
	r.Header.Set("Origin", "http://mac:8080")
	r.Header.Set("Authorization", "Bearer test-code")
	r.Header.Set("X-Upload-Ticket", p)
	// An attacker cannot impersonate the approved socket peer through these headers.
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	r.Header.Set("Forwarded", "for=192.0.2.1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("different sender used approval")
	}
	assertEmpty(t, dir)
	// The original sender can still use its permission; a wrong peer cannot consume it.
	if w := call(s, "/upload", "a", p); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestPromptLimitIncludesRejections(t *testing.T) {
	prompts := 0
	s, dir := setup(t, func(context.Context, string, int64, string) bool { prompts++; return false })
	for i := 0; i < 30; i++ {
		if w := call(s, "/request", `{"name":"a","size":1}`, ""); w.Code != 403 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := call(s, "/request", `{"name":"a","size":1}`, ""); w.Code != 429 {
		t.Fatal("prompt cap bypassed")
	}
	if prompts != 30 {
		t.Fatal("too many terminal prompts")
	}
	assertEmpty(t, dir)
}

func TestSessionEndingDuringApproval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, dir := setup(t, func(context.Context, string, int64, string) bool { cancel(); return true })
	s.ctx = ctx
	if w := call(s, "/request", `{"name":"a","size":1}`, ""); w.Code != 403 {
		t.Fatal("ended session issued a permit")
	}
	if s.pending != nil {
		t.Fatal("permission survived session cancellation")
	}
	assertEmpty(t, dir)
}
