package main

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"sync"
)

type endpoint struct {
	ip   netip.Addr
	port uint16
}

var clientAddress = endpoint{netip.MustParseAddr("192.0.2.1"), 40000}
var serverAddress = endpoint{netip.MustParseAddr("192.0.2.2"), 8080}

type network struct {
	toClient, toServer chan []byte
	mu                 sync.Mutex
	out                io.Writer
	drop               string
	dropped            bool
	dataEnd            uint32
}

func newNetwork(out io.Writer, drop string) *network {
	return &network{toClient: make(chan []byte, 16), toServer: make(chan []byte, 16), out: out, drop: drop}
}
func (n *network) log(format string, args ...any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fmt.Fprintf(n.out, format+"\n", args...)
}

// send serializes TCP inside IPv4 before the simulated link routes packet bytes.
func (n *network) send(ctx context.Context, from, to endpoint, s segment) error {
	s.source = from.port
	s.destination = to.port
	tcp, err := encodeTCP(s, from.ip, to.ip)
	if err != nil {
		return err
	}
	packet, err := encodeIPv4(tcp, from.ip, to.ip)
	if err != nil {
		return err
	}
	n.mu.Lock()
	if from == clientAddress && len(s.payload) > 0 {
		n.dataEnd = s.seq + uint32(len(s.payload))
	}
	drop := !n.dropped && ((n.drop == "data" && from == clientAddress && len(s.payload) > 0) || (n.drop == "ack" && from == serverAddress && s.flags == ack && n.dataEnd != 0 && s.acknowledgment == n.dataEnd))
	if n.drop == "all" {
		drop = true
	}
	suffix := ""
	if drop {
		n.dropped = true
		suffix = " [DROPPED]"
	}
	fmt.Fprintf(n.out, "%s -> %s  %-7s seq=%d ack=%d bytes=%d%s\n", from.ip, to.ip, flagsText(s.flags), s.seq, s.acknowledgment, len(s.payload), suffix)
	n.mu.Unlock()
	if drop {
		return nil
	}
	var destination chan []byte
	switch to.ip {
	case clientAddress.ip:
		destination = n.toClient
	case serverAddress.ip:
		destination = n.toServer
	default:
		return fmt.Errorf("no route to %s", to.ip)
	}
	select {
	case destination <- packet:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func receive(packet []byte, local, remote endpoint) (segment, error) {
	src, dst, payload, err := decodeIPv4(packet)
	if err != nil {
		return segment{}, err
	}
	if src != remote.ip || dst != local.ip {
		return segment{}, fmt.Errorf("unexpected IP endpoints")
	}
	s, err := decodeTCP(payload, src, dst)
	if err != nil {
		return segment{}, err
	}
	if s.source != remote.port || s.destination != local.port {
		return segment{}, fmt.Errorf("unexpected TCP ports")
	}
	return s, nil
}
