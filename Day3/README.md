# Day 3: Simulated IPv4 and TCP from scratch

Two endpoints exchange real encoded IPv4/TCP header bytes over Go channels. Our code performs the handshake, tracks byte sequence numbers, acknowledges data, retransmits on timeout, suppresses duplicate delivery, and closes the connection.

This is a small educational protocol subset. It does not use `net.Dial`, `net.Listen`, OS TCP, raw sockets, or the internet. `net/netip` is only used to represent addresses. No ports are opened and no administrator privileges are needed.

## Run

Requires Go 1.24 or newer. From the repository root:

```sh
cd Day3
go run .
```

By default the link drops the first data packet. The client times out, retransmits the same bytes with the same sequence number, and completes the transfer. The final output is:

```text
Delivered exactly once: "Hello from TCP!"
Both endpoints stopped.
```

Other experiments:

```sh
# No packet loss:
go run . -drop none

# Lose the first data acknowledgment; watch duplicate delivery get suppressed:
go run . -drop ack

# Send your own message:
go run . -message "Packets are just bytes." -drop data

# Drop everything and observe a bounded failure (nonzero exit status):
go run . -drop all -timeout 20ms -attempts 3
```

| Option | Default | Meaning |
| --- | --- | --- |
| `-message` | `Hello from TCP!` | At most 4096 bytes |
| `-drop` | `data` | `none`, first `data`, first data `ack`, or `all` |
| `-timeout` | `30ms` | Fixed retransmission interval, positive and at most 1 second |
| `-attempts` | `5` | Total send attempts per segment, including the original; 1–10 |

The simulation exits when complete or after bounded failure. Both endpoint routines are joined before returning; no background process remains. Goroutine scheduling can change the order of nearby trace lines.

## Read the trace

The client uses simulated address `192.0.2.1:40000`; the server uses `192.0.2.2:8080`. These addresses identify in-memory endpoints only.

```text
CLIENT -> SERVER  SYN      seq=1000 ack=0
SERVER -> CLIENT  SYN|ACK  seq=9000 ack=1001
CLIENT -> SERVER  ACK      seq=1001 ack=9001
```

SYN consumes one sequence number. The first data segment begins at 1001. It contains 12 bytes, so the receiver acknowledges 1013: **the next byte it expects**. ACK-only segments do not consume sequence numbers. FIN consumes one sequence number, like SYN.

If that acknowledgment is lost, the client resends the same segment. The server sees that its sequence number is not the next expected one, sends the current acknowledgment again, and does not append the bytes a second time.

Messages are split into at most 12-byte segments to make the trace easy to follow. TCP deals in bytes, so a UTF-8 character may span segments; concatenating delivered bytes reconstructs the original message.

## What we implement

**IPv4:** a 20-byte header with version, total length, Don't Fragment, TTL, protocol number 6, source/destination addresses, and Internet checksum. Parsing rejects truncated packets, mismatched lengths, bad checksums, expired TTL, options, fragmentation, and non-TCP payloads.

**TCP:** a 20-byte header with ports, sequence and acknowledgment numbers, SYN/ACK/PSH/FIN flags, data offset, an advertised window field, and checksum. The checksum covers the IPv4 pseudo-header, TCP header, and payload. TCP options, unsupported flags, and urgent pointers are rejected.

**Connection:** a three-way handshake, one outstanding data segment, cumulative acknowledgments, timeout retransmission, duplicate suppression, and a FIN exchange. The server combines its FIN with its acknowledgment of the client's FIN. A repeated final FIN can be acknowledged again.

**Concurrency:** the client runs in the calling goroutine and the server in another goroutine. Buffered channels carry encoded packet bytes. A mutex protects link logging and loss-injection state. A context bounds the simulation and stops the peer if the client fails.

## Files

- `packet.go`: checksums and header serialization/parsing.
- `network.go`: simulated packet routing, trace output, and loss injection.
- `connection.go`: endpoint state machines and retransmission.
- `main.go`: CLI configuration.
- `packet_test.go`, `connection_test.go`: packet integrity and connection behavior.

## Verify

```sh
go test -race ./...
go vet ./...
```

Tests cover checksum arithmetic, round trips, corruption, pseudo-header validation, truncated/unsupported packets, exact delivery under data/ACK loss, duplicate suppression, empty and maximum-length messages, wrong acknowledgments, permanent loss, cancellation, and invalid configuration.

## Deliberate differences from a complete stack

- One fixed client/server connection, with application data in one direction.
- Stop-and-wait instead of a sliding window; the advertised window field is not used for dynamic flow control.
- Fixed retransmission intervals; no RTT estimation, congestion control, or exponential backoff.
- Fixed initial sequence numbers for readable traces; unsuitable for a real network.
- No out-of-order receive buffer, retransmission queue, reset handling, TCP options, or simultaneous open/close.
- TIME-WAIT ends when the simulator observes the server closing, rather than using real TCP's 2×MSL timer.
- No Ethernet, ARP, ICMP, routing table, forwarding/TTL decrement, fragmentation, or OS integration.
- Checksums detect corruption; they do not provide authentication or encryption.

The encoded headers resemble real packets, but this state machine is not a complete TCP implementation and has not been tested for interoperability with real devices. Useful follow-up exercises are packet reordering, a sliding window, adaptive retransmission timers, and more complete teardown behavior.
