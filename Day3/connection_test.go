package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDeliveryWithLoss(t *testing.T) {
	for _, drop := range []string{"none", "data", "ack"} {
		t.Run(drop, func(t *testing.T) {
			var trace bytes.Buffer
			message := []byte("Hello from TCP! Unicode: 猫🙂 and multiple segments.")
			received, err := simulate(context.Background(), &trace, message, config{10 * time.Millisecond, 5, drop})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(received, message) {
				t.Fatalf("delivery changed: %q", received)
			}
			if drop != "none" && !strings.Contains(trace.String(), "retransmit") {
				t.Fatal("loss did not cause retransmission")
			}
			if drop == "ack" && !strings.Contains(trace.String(), "without delivering again") {
				t.Fatal("duplicate suppression not exercised")
			}
			if !strings.Contains(trace.String(), "SERVER LAST-ACK -> CLOSED") || !strings.Contains(trace.String(), "CLIENT TIME-WAIT -> CLOSED") {
				t.Fatal("endpoints did not close")
			}
		})
	}
}

func TestEmptyAndMaximumMessage(t *testing.T) {
	for _, message := range []string{"", strings.Repeat("a", 4096)} {
		got, err := simulate(context.Background(), io.Discard, []byte(message), config{20 * time.Millisecond, 5, "none"})
		if err != nil || string(got) != message {
			t.Fatal("boundary message failed", err)
		}
	}
}

func TestTimeoutCancellationAndValidation(t *testing.T) {
	if _, err := simulate(context.Background(), io.Discard, []byte("x"), config{time.Millisecond, 2, "all"}); err == nil {
		t.Fatal("permanent loss succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := simulate(ctx, io.Discard, []byte("x"), config{time.Millisecond, 2, "all"}); err == nil {
		t.Fatal("canceled simulation succeeded")
	}
	for _, c := range []config{{0, 2, "none"}, {time.Millisecond, 0, "none"}, {time.Millisecond, 2, "invalid"}} {
		if _, err := simulate(context.Background(), io.Discard, nil, c); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if _, err := simulate(context.Background(), io.Discard, make([]byte, 4097), config{time.Millisecond, 2, "none"}); err == nil {
		t.Fatal("oversized message accepted")
	}
}

func TestExchangeIgnoresWrongAcknowledgment(t *testing.T) {
	n := newNetwork(io.Discard, "none")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, number := range []uint32{99, 104} {
		if err := n.send(ctx, serverAddress, clientAddress, segment{seq: 9001, acknowledgment: number, flags: ack}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := exchange(ctx, n, config{time.Millisecond, 2, "none"}, segment{seq: 101, payload: []byte("abc"), flags: psh | ack}, func(s segment) bool { return s.acknowledgment == 104 })
	if err != nil || s.acknowledgment != 104 {
		t.Fatal("accepted wrong acknowledgment", err)
	}
}
