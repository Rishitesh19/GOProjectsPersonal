package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSharing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello from Go"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	handler := fileHandler(root)

	for _, tc := range []struct {
		name, method, path string
		status             int
		body               string
	}{
		{"listing", "GET", "/", 200, "hello.txt"},
		{"download", "GET", "/hello.txt", 200, "hello from Go"},
		{"head", "HEAD", "/hello.txt", 200, ""},
		{"missing", "GET", "/missing.txt", 404, ""},
		{"upload rejected", "POST", "/hello.txt", 405, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.body) {
				t.Fatalf("got %d %q", response.Code, response.Body.String())
			}
			if tc.method == "HEAD" && response.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
		})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/escape.txt", nil))
	if response.Code < 400 || strings.Contains(response.Body.String(), "private contents") {
		t.Fatal("symlink exposed a file outside the shared folder")
	}
}
