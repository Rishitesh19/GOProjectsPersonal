package main

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("secret contents"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	handler := fileHandler(root)
	for _, path := range []string{"/", "/private.txt", "/../private.txt"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if (path == "/" && strings.Contains(response.Body.String(), "private.txt")) || strings.Contains(response.Body.String(), "secret contents") {
			t.Fatal("exposed private file")
		}
		if path == "/private.txt" && response.Code != 404 {
			t.Fatal("file is accessible")
		}
	}
	for i := 0; i < 2; i++ {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, err := writer.CreateFormFile("file", "private.txt")
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte("from phone"))
		writer.Close()
		request := httptest.NewRequest("POST", "/upload", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 303 {
			t.Fatalf("upload: %d %s", response.Code, response.Body.String())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatalf("expected original plus two uploads, got %d", len(entries))
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		want := "from phone"
		if entry.Name() == "private.txt" {
			want = "secret contents"
		}
		if string(content) != want {
			t.Fatalf("unexpected content: %q", content)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/upload", strings.NewReader("bad multipart")))
	if response.Code != 400 {
		t.Fatal("invalid upload accepted")
	}
}
