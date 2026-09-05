package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	dir := flag.String("dir", "", "folder to receive uploads (required)")
	port := flag.Int("port", 8080, "port to listen on")
	lifetime := flag.Duration("lifetime", 10*time.Minute, "stop automatically after this duration")
	quota := flag.Int64("quota-mb", 200, "maximum MiB received per session")
	once := flag.Bool("once", false, "stop after one successful upload")
	flag.Parse()
	if *dir == "" || flag.NArg() != 0 || *port < 1 || *port > 65535 || *lifetime <= 0 || *quota < 1 || *quota > 10240 {
		fmt.Fprintln(os.Stderr, "Usage: go run . -dir ./received [-port 8080] [-lifetime 10m] [-quota-mb 200] [-once]")
		os.Exit(1)
	}
	if err := serve(*dir, *port, *lifetime, *quota<<20, *once); err != nil {
		log.Fatal(err)
	}
}

func serve(dir string, port int, lifetime time.Duration, quota int64, once bool) error {
	path, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	defer root.Close()
	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, lifetime)
	defer cancel()
	code := rand.Text()[:12]
	receiver := newReceiver(root, code, quota, terminalApproval(), ctx)
	hosts := map[string]bool{fmt.Sprintf("localhost:%d", port): true, fmt.Sprintf("127.0.0.1:%d", port): true}
	fmt.Printf("Receiving into: %s\nLocal: http://localhost:%d\n", path, port)
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
			host := fmt.Sprintf("%s:%d", network.IP, port)
			hosts[host] = true
			fmt.Printf("Network: http://%s\n", host)
		}
	}
	receiver.hosts = hosts
	if once {
		receiver.onSuccess = cancel
	}
	fmt.Printf("Access code: %s\nSession ends in %s. Quota: %d MiB.\n", code, lifetime, quota>>20)
	fmt.Println("Keep this terminal open: every file needs your approval here.")
	fmt.Println("HTTP is unencrypted. Use trusted Wi-Fi. Ctrl+C stops the server.")
	server := &http.Server{Handler: receiver, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 3 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, finish := context.WithTimeout(context.Background(), 5*time.Second)
		defer finish()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
		}
	}()
	err = server.Serve(listener)
	cancel()
	<-done
	fmt.Println("Server stopped; this session's code and approvals have expired.")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// One reader owns stdin. Approval IDs prevent a late answer approving a later file.
func terminalApproval() approvalFunc {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines <- strings.TrimSpace(scanner.Text())
		}
	}()
	return func(ctx context.Context, name string, size int64, peer string) bool {
		id := rand.Text()[:6]
		fmt.Printf("\nUpload request from %s: %q (%d bytes)\nType yes %s to accept, or no %s to reject (60 seconds):\n", peer, name, size, id, id)
		for {
			select {
			case <-ctx.Done():
				fmt.Println("Approval expired.")
				return false
			case line, ok := <-lines:
				if !ok {
					return false
				}
				if line == "yes "+id {
					return true
				}
				if line == "no "+id {
					return false
				}
				fmt.Printf("Enter yes %s or no %s for this request.\n", id, id)
			}
		}
	}
}
