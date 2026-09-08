package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
)

const (
	fin byte = 0x01
	syn byte = 0x02
	ack byte = 0x10
	psh byte = 0x08
)

type segment struct {
	source, destination uint16
	seq, acknowledgment uint32
	flags               byte
	payload             []byte
}

// Internet checksum: one's-complement sum of 16-bit words, then complement.
func checksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func pseudoHeader(src, dst netip.Addr, length int) []byte {
	pseudo := make([]byte, 12)
	a, b := src.As4(), dst.As4()
	copy(pseudo[:4], a[:])
	copy(pseudo[4:8], b[:])
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:], uint16(length))
	return pseudo
}

func encodeTCP(s segment, src, dst netip.Addr) ([]byte, error) {
	if !src.Is4() || !dst.Is4() || len(s.payload) > 65535-40 {
		return nil, fmt.Errorf("invalid IPv4 address or oversized payload")
	}
	data := make([]byte, 20+len(s.payload))
	binary.BigEndian.PutUint16(data[0:2], s.source)
	binary.BigEndian.PutUint16(data[2:4], s.destination)
	binary.BigEndian.PutUint32(data[4:8], s.seq)
	binary.BigEndian.PutUint32(data[8:12], s.acknowledgment)
	data[12] = 5 << 4
	data[13] = s.flags
	binary.BigEndian.PutUint16(data[14:16], 4096)
	copy(data[20:], s.payload)
	binary.BigEndian.PutUint16(data[16:18], checksum(append(pseudoHeader(src, dst, len(data)), data...)))
	return data, nil
}

func decodeTCP(data []byte, src, dst netip.Addr) (segment, error) {
	if !src.Is4() || !dst.Is4() || len(data) < 20 || len(data) > 65535 {
		return segment{}, fmt.Errorf("invalid TCP length or address")
	}
	if data[12] != 5<<4 {
		return segment{}, fmt.Errorf("TCP options and reserved bits are unsupported")
	}
	if data[13]&^(fin|syn|ack|psh) != 0 || binary.BigEndian.Uint16(data[18:20]) != 0 {
		return segment{}, fmt.Errorf("unsupported TCP flags or urgent pointer")
	}
	if checksum(append(pseudoHeader(src, dst, len(data)), data...)) != 0 {
		return segment{}, fmt.Errorf("bad TCP checksum")
	}
	return segment{source: binary.BigEndian.Uint16(data[:2]), destination: binary.BigEndian.Uint16(data[2:4]), seq: binary.BigEndian.Uint32(data[4:8]), acknowledgment: binary.BigEndian.Uint32(data[8:12]), flags: data[13], payload: append([]byte(nil), data[20:]...)}, nil
}

func encodeIPv4(payload []byte, src, dst netip.Addr) ([]byte, error) {
	if !src.Is4() || !dst.Is4() || len(payload) > 65535-20 {
		return nil, fmt.Errorf("invalid IPv4 address or oversized packet")
	}
	packet := make([]byte, 20+len(payload))
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[6] = 0x40 // Don't Fragment; this stack does not fragment or reassemble.
	packet[8] = 64
	packet[9] = 6
	a, b := src.As4(), dst.As4()
	copy(packet[12:16], a[:])
	copy(packet[16:20], b[:])
	binary.BigEndian.PutUint16(packet[10:12], checksum(packet[:20]))
	copy(packet[20:], payload)
	return packet, nil
}

func decodeIPv4(packet []byte) (netip.Addr, netip.Addr, []byte, error) {
	invalid := func(message string) (netip.Addr, netip.Addr, []byte, error) {
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("%s", message)
	}
	if len(packet) < 20 || packet[0] != 0x45 {
		return invalid("need IPv4 without options")
	}
	if int(binary.BigEndian.Uint16(packet[2:4])) != len(packet) {
		return invalid("bad IPv4 total length")
	}
	if binary.BigEndian.Uint16(packet[6:8])&0xbfff != 0 {
		return invalid("IPv4 fragmentation or reserved flag unsupported")
	}
	if packet[8] == 0 || packet[9] != 6 {
		return invalid("expired TTL or non-TCP packet")
	}
	if checksum(packet[:20]) != 0 {
		return invalid("bad IPv4 checksum")
	}
	src := netip.AddrFrom4([4]byte(packet[12:16]))
	dst := netip.AddrFrom4([4]byte(packet[16:20]))
	return src, dst, packet[20:], nil
}

func flagsText(flags byte) string {
	var names []string
	for _, item := range []struct {
		flag byte
		name string
	}{{syn, "SYN"}, {fin, "FIN"}, {psh, "PSH"}, {ack, "ACK"}} {
		if flags&item.flag != 0 {
			names = append(names, item.name)
		}
	}
	return strings.Join(names, "|")
}
