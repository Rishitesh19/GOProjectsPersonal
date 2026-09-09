package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("day4", flag.ContinueOnError)
	flags.SetOutput(errOut)
	value := flags.String("value", "0.1", "number to encode, including -0, +Inf, -Inf, or NaN")
	input := flags.String("read", "", "read exactly eight bytes from a local file")
	output := flags.String("write", "", "save eight big-endian bytes to a NEW file")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	explicitValue := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			explicitValue = true
		}
	})
	if flags.NArg() != 0 || (*input != "" && explicitValue) {
		return fmt.Errorf("use -value OR -read, optionally with -write; see -h")
	}
	var bits binary64
	if *input != "" {
		file, err := os.Open(*input)
		if err != nil {
			return err
		}
		bits, err = readBinary64(file)
		file.Close()
		if err != nil {
			return err
		}
	} else {
		x, err := strconv.ParseFloat(*value, 64)
		if err != nil {
			return fmt.Errorf("invalid float64: %w", err)
		}
		bits = encode(x)
	}
	if *output != "" {
		// Never overwrite an existing file, including symlinks.
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		writeErr := writeBinary64(file, bits)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(*output)
			if writeErr != nil {
				return writeErr
			}
			return closeErr
		}
	}
	sign, exponent, fraction := bits.fields()
	data := bits.bytes()
	if _, err := fmt.Fprintf(out, "Value: %.17g\nClass: %s\nSign: %d\nExponent: %011b (%d stored)\nFraction: %052b\nBits: %016x\nBytes (big-endian): % x\n", bits.decode(), bits.class(), sign, exponent, exponent, fraction, uint64(bits), data); err != nil {
		return err
	}
	if bits.class() == "normal" {
		if _, err := fmt.Fprintf(out, "Formula: (-1)^%d × (1 + %d/2^52) × 2^(%d)\n", sign, fraction, int(exponent)-1023); err != nil {
			return err
		}
	} else if bits.class() == "subnormal" {
		if _, err := fmt.Fprintf(out, "Formula: (-1)^%d × %d × 2^-1074\n", sign, fraction); err != nil {
			return err
		}
	}
	if *output != "" {
		_, err := fmt.Fprintf(out, "Saved 8 bytes to %s\n", *output)
		return err
	}
	return nil
}
