package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

type config struct {
	timeout  time.Duration
	attempts int
	drop     string
}
type serverResult struct {
	data []byte
	err  error
}

// exchange resends the same sequence range until a matching response arrives.
// Unrelated or corrupt packets do not reset the retransmission deadline.
func exchange(ctx context.Context, n *network, c config, out segment, match func(segment) bool) (segment, error) {
	for attempt := 1; attempt <= c.attempts; attempt++ {
		if attempt > 1 {
			n.log("CLIENT retransmit attempt %d/%d", attempt, c.attempts)
		}
		if err := n.send(ctx, clientAddress, serverAddress, out); err != nil {
			return segment{}, err
		}
		timer := time.NewTimer(c.timeout)
	wait:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return segment{}, ctx.Err()
			case <-timer.C:
				break wait
			case packet := <-n.toClient:
				response, err := receive(packet, clientAddress, serverAddress)
				if err != nil {
					n.log("CLIENT discarded packet: %v", err)
					continue
				}
				if match(response) {
					timer.Stop()
					return response, nil
				}
			}
		}
	}
	return segment{}, fmt.Errorf("no matching response after %d attempts", c.attempts)
}

func client(ctx context.Context, n *network, c config, message []byte, serverDone <-chan struct{}) error {
	next := uint32(1000)
	n.log("CLIENT CLOSED -> SYN-SENT")
	response, err := exchange(ctx, n, c, segment{seq: next, flags: syn}, func(s segment) bool { return s.flags == syn|ack && s.acknowledgment == next+1 && len(s.payload) == 0 })
	if err != nil {
		return err
	}
	next++
	peerNext := response.seq + 1
	if err = n.send(ctx, clientAddress, serverAddress, segment{seq: next, acknowledgment: peerNext, flags: ack}); err != nil {
		return err
	}
	n.log("CLIENT SYN-SENT -> ESTABLISHED")
	for offset := 0; offset < len(message); {
		end := offset + 12
		if end > len(message) {
			end = len(message)
		}
		payload := message[offset:end]
		expected := next + uint32(len(payload))
		_, err = exchange(ctx, n, c, segment{seq: next, acknowledgment: peerNext, flags: psh | ack, payload: payload}, func(s segment) bool {
			return s.flags == ack && s.acknowledgment == expected && s.seq == peerNext && len(s.payload) == 0
		})
		if err != nil {
			return err
		}
		next = expected
		offset = end
	}
	n.log("CLIENT ESTABLISHED -> FIN-WAIT-1")
	response, err = exchange(ctx, n, c, segment{seq: next, acknowledgment: peerNext, flags: fin | ack}, func(s segment) bool {
		return s.flags == fin|ack && s.acknowledgment == next+1 && s.seq == peerNext && len(s.payload) == 0
	})
	if err != nil {
		return err
	}
	next++
	peerNext++
	finalACK := segment{seq: next, acknowledgment: peerNext, flags: ack}
	if err = n.send(ctx, clientAddress, serverAddress, finalACK); err != nil {
		return err
	}
	n.log("CLIENT FIN-WAIT-1 -> TIME-WAIT (simulated)")
	// Stay available to acknowledge a repeated FIN if the final ACK was lost.
	// The simulator can observe peer shutdown; real TCP uses a TIME-WAIT timer.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-serverDone:
			n.log("CLIENT TIME-WAIT -> CLOSED")
			return nil
		case packet := <-n.toClient:
			s, err := receive(packet, clientAddress, serverAddress)
			if err == nil && s.flags == fin|ack && s.seq+1 == peerNext && s.acknowledgment == next {
				if err = n.send(ctx, clientAddress, serverAddress, finalACK); err != nil {
					return err
				}
			}
		}
	}
}

func server(ctx context.Context, n *network, c config) ([]byte, error) {
	state := "LISTEN"
	n.log("SERVER LISTEN")
	next, peerNext := uint32(9000), uint32(0)
	var data []byte
	var lastFIN segment
	var retry <-chan time.Time
	var timer *time.Timer
	attempts := 0
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	sendACK := func() error {
		return n.send(ctx, serverAddress, clientAddress, segment{seq: next, acknowledgment: peerNext, flags: ack})
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-retry:
			if attempts >= c.attempts {
				return nil, fmt.Errorf("server final ACK timed out")
			}
			attempts++
			n.log("SERVER retransmit FIN attempt %d/%d", attempts, c.attempts)
			if err := n.send(ctx, serverAddress, clientAddress, lastFIN); err != nil {
				return nil, err
			}
			timer.Reset(c.timeout)
		case packet := <-n.toServer:
			s, err := receive(packet, serverAddress, clientAddress)
			if err != nil {
				n.log("SERVER discarded packet: %v", err)
				continue
			}
			if state == "LISTEN" {
				if s.flags != syn || len(s.payload) != 0 {
					continue
				}
				peerNext = s.seq + 1
				state = "SYN-RECEIVED"
				next++
				n.log("SERVER LISTEN -> SYN-RECEIVED")
			}
			if state == "SYN-RECEIVED" {
				if s.flags == syn && s.seq+1 == peerNext && len(s.payload) == 0 {
					if err = n.send(ctx, serverAddress, clientAddress, segment{seq: next - 1, acknowledgment: peerNext, flags: syn | ack}); err != nil {
						return nil, err
					}
					continue
				}
				if s.flags&ack == 0 || s.acknowledgment != next || s.seq != peerNext {
					continue
				}
				state = "ESTABLISHED"
				n.log("SERVER SYN-RECEIVED -> ESTABLISHED")
			}
			if state == "LAST-ACK" {
				if s.flags == ack && s.seq == peerNext && s.acknowledgment == next && len(s.payload) == 0 {
					n.log("SERVER LAST-ACK -> CLOSED")
					return data, nil
				}
				if s.flags == fin|ack && s.seq+1 == peerNext && len(s.payload) == 0 {
					if err = n.send(ctx, serverAddress, clientAddress, lastFIN); err != nil {
						return nil, err
					}
				}
				continue
			}
			if s.acknowledgment != next {
				continue
			}
			if s.flags == psh|ack && len(s.payload) > 0 {
				if s.seq == peerNext {
					if len(data)+len(s.payload) > 4096 {
						return nil, fmt.Errorf("receiver limit exceeded")
					}
					data = append(data, s.payload...)
					peerNext += uint32(len(s.payload))
				} else {
					n.log("SERVER duplicate/out-of-order data: acknowledge %d without delivering again", peerNext)
				}
				if err = sendACK(); err != nil {
					return nil, err
				}
			} else if s.flags == fin|ack && len(s.payload) == 0 && s.seq == peerNext {
				peerNext++
				n.log("SERVER ESTABLISHED -> CLOSE-WAIT -> LAST-ACK")
				lastFIN = segment{seq: next, acknowledgment: peerNext, flags: fin | ack}
				next++
				if err = n.send(ctx, serverAddress, clientAddress, lastFIN); err != nil {
					return nil, err
				}
				state = "LAST-ACK"
				attempts = 1
				timer = time.NewTimer(c.timeout)
				retry = timer.C
			}
		}
	}
}

func simulate(parent context.Context, out io.Writer, message []byte, c config) ([]byte, error) {
	if len(message) > 4096 || c.timeout <= 0 || c.timeout > time.Second || c.attempts < 1 || c.attempts > 10 {
		return nil, fmt.Errorf("message <= 4096 bytes, timeout > 0 and <= 1s, attempts 1–10 required")
	}
	if c.drop != "none" && c.drop != "data" && c.drop != "ack" && c.drop != "all" {
		return nil, fmt.Errorf("drop must be none, data, ack, or all")
	}
	ctx, cancel := context.WithTimeout(parent, time.Second+4*c.timeout*time.Duration(c.attempts+1))
	defer cancel()
	n := newNetwork(out, c.drop)
	serverDone := make(chan struct{})
	result := make(chan serverResult, 1)
	go func() { data, err := server(ctx, n, c); result <- serverResult{data, err}; close(serverDone) }()
	err := client(ctx, n, c, message, serverDone)
	if err != nil {
		cancel()
	}
	received := <-result
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}
	if received.err != nil {
		return nil, fmt.Errorf("server: %w", received.err)
	}
	return received.data, nil
}
