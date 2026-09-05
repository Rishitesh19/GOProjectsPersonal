package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	dir := flag.String("dir", "", "folder to receive uploads (required)")
	port := flag.Int("port", 8080, "port to listen on")
	flag.Parse()

	if *dir == "" || flag.NArg() != 0 || *port < 1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "Usage: go run . -dir /path/to/folder [-port 8080]")
		os.Exit(1)
	}
	if err := serve(*dir, *port); err != nil {
		log.Fatal(err)
	}
}

func serve(dir string, port int) error {
	path, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// OpenRoot keeps file access inside the selected folder, including symlinks.
	root, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("open shared folder: %w", err)
	}
	defer root.Close()

	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}
	defer listener.Close()

	fmt.Printf("Saving uploads to: %s\nLocal:   http://localhost:%d\n", path, port)
	addresses, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
				fmt.Printf("Network: http://%s:%d\n", network.IP, port)
			}
		}
	}
	fmt.Println("Upload-only: files are never listed or served. Use a trusted network.")
	fmt.Println("Press Ctrl+C to stop receiving.")

	server := &http.Server{
		Handler:           fileHandler(root),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server.Serve(listener)
}

const uploadPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Send to Mac</title><style>
body{font:18px system-ui;max-width:480px;margin:64px auto;padding:24px;background:#f4f6f8;color:#17212b}
form{background:white;padding:24px;border-radius:16px}input{display:block;margin:24px 0;max-width:100%}
button{background:#176b45;color:white;border:0;border-radius:8px;padding:14px 24px;font:inherit}
p{line-height:1.5}</style></head><body><h1>Send a file to your Mac</h1>
<p>Choose a photo or file from your phone. Files on your Mac are never shown here.</p>
<form action="/upload" method="post" enctype="multipart/form-data">
<label for="file">One file, up to 50 MB</label><input id="file" name="file" type="file" required>
<button type="submit">Send to Mac</button></form><p>Keep this page open until the upload finishes.</p></body></html>`

func fileHandler(root *os.Root) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, uploadPage)
	})
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		// Bound the whole request, including multipart overhead, before parsing it.
		r.Body = http.MaxBytesReader(w, r.Body, 51<<20)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			if r.MultipartForm != nil {
				r.MultipartForm.RemoveAll()
			}
			http.Error(w, "Could not read upload. Choose a file up to 50 MB.", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		files := r.MultipartForm.File["file"]
		if len(files) != 1 || len(r.MultipartForm.File) != 1 {
			http.Error(w, "Choose exactly one file.", http.StatusBadRequest)
			return
		}
		header := files[0]
		if header.Size > 50<<20 {
			http.Error(w, "File exceeds 50 MB.", http.StatusRequestEntityTooLarge)
			return
		}
		source, err := header.Open()
		if err != nil {
			http.Error(w, "Could not read file.", 500)
			return
		}
		defer source.Close()
		// Keep a recognizable name; a random prefix avoids overwriting earlier uploads.
		name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
		name = strings.Map(func(c rune) rune {
			if c < 32 || c == 127 {
				return -1
			}
			return c
		}, name)
		if name == "" || name == "." || name == ".." || len(name) > 180 {
			http.Error(w, "Choose a shorter, valid filename.", 400)
			return
		}
		name = rand.Text() + "-" + name
		destination, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			http.Error(w, "Could not save file on Mac.", 500)
			return
		}
		_, copyErr := io.Copy(destination, source)
		closeErr := destination.Close()
		if copyErr != nil || closeErr != nil {
			root.Remove(name)
			http.Error(w, "Upload failed. Please try again.", 500)
			return
		}
		log.Printf("Received %s (%d bytes)", name, header.Size)
		http.Redirect(w, r, "/done", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /done", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html lang="en"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Upload complete</title><body style="font:20px system-ui;padding:32px"><h1>File received</h1><p>Your file was saved on the Mac.</p><a href="/">Send another file</a></body></html>`)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}
