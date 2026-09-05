package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

//go:embed page.html
var page string

//go:embed page.js
var script string

const maxFile = 50 << 20

type approvalFunc func(context.Context, string, int64, string) bool

type offer struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}
type permit struct {
	offer
	peerIP  string
	ticket  string
	expires time.Time
}
type receiver struct {
	root        *os.Root
	code        string
	quota, used int64
	count       int
	prompts     int
	approve     approvalFunc
	ctx         context.Context
	hosts       map[string]bool
	onSuccess   func()
	mu          sync.Mutex
	busy        bool
	pending     *permit
	failures    int
	lockedUntil time.Time
}

func newReceiver(root *os.Root, code string, quota int64, approve approvalFunc, ctx context.Context) *receiver {
	return &receiver{root: root, code: code, quota: quota, approve: approve, ctx: ctx}
}

func (s *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
	if s.ctx.Err() != nil {
		http.Error(w, "Session ended.", 503)
		return
	}
	if !s.hosts[r.Host] {
		http.Error(w, "Unknown server address.", 403)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/page.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		fmt.Fprint(w, script)
		return
	}
	if r.URL.Path != "/request" && r.URL.Path != "/upload" {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required.", 405)
		return
	}
	if r.Header.Get("Origin") != "http://"+r.Host || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		http.Error(w, "Open the upload page directly on this server.", 403)
		return
	}
	if !s.authenticate(w, r) {
		return
	}
	if r.URL.Path == "/request" {
		s.request(w, r)
	} else {
		s.upload(w, r)
	}
}

func (s *receiver) authenticate(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.lockedUntil) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many incorrect codes. Wait one minute.", 429)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.code)) != 1 {
		s.failures++
		if s.failures >= 5 {
			s.lockedUntil = time.Now().Add(time.Minute)
			s.failures = 0
		}
		http.Error(w, "Incorrect access code.", 401)
		return false
	}
	return true
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 180 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return false
		}
	}
	return true
}

func (s *receiver) request(w http.ResponseWriter, r *http.Request) {
	// Trust only the socket address, never a client-supplied forwarding header.
	peerIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "Cannot identify sender address.", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var item offer
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&item) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validName(item.Name) || item.Size < 0 || item.Size > maxFile {
		http.Error(w, "Choose one file up to 50 MiB with a valid filename.", 400)
		return
	}
	s.mu.Lock()
	if s.pending != nil && time.Now().After(s.pending.expires) {
		s.pending = nil
	}
	if s.busy || s.pending != nil {
		s.mu.Unlock()
		http.Error(w, "Another upload is awaiting approval or transfer. Try again shortly.", 409)
		return
	}
	if item.Size > s.quota-s.used || s.count >= 20 {
		s.mu.Unlock()
		http.Error(w, "Session quota reached (bytes or 20 files).", 413)
		return
	}
	if s.prompts >= 30 {
		s.mu.Unlock()
		http.Error(w, "Session approval limit reached. Restart on the Mac to accept more requests.", 429)
		return
	}
	s.prompts++
	s.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if !s.approve(ctx, item.Name, item.Size, r.RemoteAddr) || ctx.Err() != nil || s.ctx.Err() != nil {
		http.Error(w, "Upload declined or approval expired. Nothing was saved.", 403)
		return
	}
	ticket := rand.Text()
	s.mu.Lock()
	s.pending = &permit{offer: item, peerIP: peerIP, ticket: ticket, expires: time.Now().Add(60 * time.Second)}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}

func (s *receiver) upload(w http.ResponseWriter, r *http.Request) {
	peerIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "Cannot identify sender address.", 400)
		return
	}
	s.mu.Lock()
	p := s.pending
	if s.busy || p == nil || peerIP != p.peerIP || time.Now().After(p.expires) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Upload-Ticket")), []byte(p.ticket)) != 1 {
		s.mu.Unlock()
		http.Error(w, "Request a new approval before uploading.", 403)
		return
	}
	// A ticket is consumed even if transfer fails; every retry needs fresh consent.
	s.pending = nil
	s.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
	if r.ContentLength >= 0 && r.ContentLength != p.Size {
		http.Error(w, "File size differs from approved size.", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, p.Size+1)
	name := rand.Text() + "-" + p.Name
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		http.Error(w, "Could not create file on Mac.", 500)
		return
	}
	n, copyErr := io.Copy(file, r.Body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || n != p.Size || s.ctx.Err() != nil {
		if err := s.root.Remove(name); err != nil {
			log.Printf("Could not remove incomplete upload %q: %v", name, err)
		}
		http.Error(w, "Transfer failed; request approval again.", 400)
		return
	}
	s.mu.Lock()
	s.used += n
	s.count++
	s.mu.Unlock()
	log.Printf("Saved approved upload %q (%d bytes)", name, n)
	fmt.Fprintln(w, "File received and saved on your Mac.")
	if s.onSuccess != nil {
		s.onSuccess()
	}
}
