package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := flag.String("dir", "", "folder to share (required)")
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

	fmt.Printf("Sharing: %s\nLocal:   http://localhost:%d\n", path, port)
	addresses, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
				fmt.Printf("Network: http://%s:%d\n", network.IP, port)
			}
		}
	}
	fmt.Println("Anyone who can reach this port can read this folder. Use a trusted network.")
	fmt.Println("Press Ctrl+C to stop sharing.")

	server := &http.Server{
		Handler:           fileHandler(root),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server.Serve(listener)
}

func fileHandler(root *os.Root) http.Handler {
	files := http.FileServerFS(root.FS())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Only browsing and downloading are supported", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Shared HTML files should not run scripts in the server's browser origin.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
		files.ServeHTTP(w, r)
	})
}
