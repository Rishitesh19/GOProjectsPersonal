package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("day3", flag.ContinueOnError)
	flags.SetOutput(errOut)
	message := flags.String("message", "Hello from TCP!", "message to transfer, at most 4096 bytes")
	drop := flags.String("drop", "data", "drop none, first data segment, first data ack, or all packets: none|data|ack|all")
	timeout := flags.Duration("timeout", 30*time.Millisecond, "retransmission interval, at most 1s")
	attempts := flags.Int("attempts", 5, "maximum send attempts per segment, 1–10")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; use -h")
	}
	fmt.Fprintln(out, "Simulated IPv4 + TCP: channels carry packet bytes. No sockets or real network traffic.")
	received, err := simulate(context.Background(), out, []byte(*message), config{*timeout, *attempts, *drop})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\nDelivered exactly once: %q\nBoth endpoints stopped.\n", received)
	return err
}
