package main

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestChecksum(t *testing.T) {
	// Hand-computable cases cover odd bytes and end-around carry.
	for _, tc := range []struct {
		data []byte
		want uint16
	}{
		{nil, 0xffff}, {[]byte{0, 1}, 0xfffe}, {[]byte{1}, 0xfeff}, {[]byte{0xff, 0xff, 0, 1}, 0xfffe},
	} {
		if got := checksum(tc.data); got != tc.want {
			t.Fatalf("checksum %x: %x != %x", tc.data, got, tc.want)
		}
	}
}

func TestPacketRoundTripAndCorruption(t *testing.T) {
	src, dst := clientAddress.ip, serverAddress.ip
	original := segment{source: 40000, destination: 8080, seq: 0xfffffffe, acknowledgment: 12, flags: psh | ack, payload: []byte("odd")}
	tcp, err := encodeTCP(original, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := encodeIPv4(tcp, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	a, b, payload, err := decodeIPv4(packet)
	if err != nil || a != src || b != dst {
		t.Fatal("IPv4 round trip failed", err)
	}
	decoded, err := decodeTCP(payload, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.seq != original.seq || decoded.acknowledgment != 12 || decoded.source != 40000 || decoded.destination != 8080 || decoded.flags != original.flags || !bytes.Equal(decoded.payload, original.payload) {
		t.Fatal("TCP fields changed")
	}
	damaged := append([]byte(nil), packet...)
	damaged[8] ^= 1
	if _, _, _, err := decodeIPv4(damaged); err == nil {
		t.Fatal("bad IPv4 checksum accepted")
	}
	damaged = append([]byte(nil), tcp...)
	damaged[len(damaged)-1] ^= 1
	if _, err := decodeTCP(damaged, src, dst); err == nil {
		t.Fatal("bad TCP checksum accepted")
	}
	if _, err := decodeTCP(tcp, netip.MustParseAddr("192.0.2.3"), dst); err == nil {
		t.Fatal("pseudo-header not checked")
	}
}

func TestRejectUnsupportedAndTruncatedPackets(t *testing.T) {
	tcp, _ := encodeTCP(segment{flags: syn}, clientAddress.ip, serverAddress.ip)
	packet, _ := encodeIPv4(tcp, clientAddress.ip, serverAddress.ip)
	for i := 0; i < len(packet); i++ {
		if _, _, _, err := decodeIPv4(packet[:i]); err == nil {
			t.Fatalf("truncated packet length %d accepted", i)
		}
	}
	for i := 0; i < 20; i++ {
		if _, err := decodeTCP(tcp[:i], clientAddress.ip, serverAddress.ip); err == nil {
			t.Fatal("short TCP header accepted")
		}
	}
	for _, mutation := range []func([]byte){
		func(p []byte) { p[0] = 0x65 }, func(p []byte) { p[0] = 0x46 }, func(p []byte) { p[6] = 0x20 }, func(p []byte) { p[7] = 1 }, func(p []byte) { p[8] = 0 }, func(p []byte) { p[9] = 17 },
	} {
		p := append([]byte(nil), packet...)
		mutation(p)
		p[10] = 0
		p[11] = 0
		binary.BigEndian.PutUint16(p[10:12], checksum(p[:20]))
		if _, _, _, err := decodeIPv4(p); err == nil {
			t.Fatal("unsupported IP header accepted")
		}
	}
	if _, err := encodeIPv4(make([]byte, 65516), clientAddress.ip, serverAddress.ip); err == nil {
		t.Fatal("oversized IP packet accepted")
	}
	if _, err := encodeTCP(segment{}, netip.MustParseAddr("::1"), serverAddress.ip); err == nil {
		t.Fatal("IPv6 accepted")
	}
}
