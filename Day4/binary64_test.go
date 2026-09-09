package main

import (
	"bytes"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Go's bit-conversion helpers appear only in tests, as an independent oracle.
func TestAgainstGo(t *testing.T) {
	cases := []uint64{0, signMask, 1, signMask | 1, 0x000fffffffffffff, 0x0010000000000000, 0x3ff0000000000000, 0x3fb999999999999a, 0x7fefffffffffffff, 0xffefffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, canonicalNaN, 0x7ff0000000000001, 0xfff8000000000042}
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 10000; i++ {
		cases = append(cases, rng.Uint64())
	}
	for _, bits := range cases {
		expected := math.Float64frombits(bits)
		encoded := uint64(encode(expected))
		if math.IsNaN(expected) {
			if encoded != canonicalNaN || !math.IsNaN(binary64(bits).decode()) {
				t.Fatalf("NaN handling: %x", bits)
			}
		} else {
			if encoded != bits {
				t.Fatalf("encode: got %x want %x", encoded, bits)
			}
			if got := math.Float64bits(binary64(bits).decode()); got != bits {
				t.Fatalf("decode: got %x want %x", got, bits)
			}
		}
		var stored bytes.Buffer
		if err := writeBinary64(&stored, binary64(bits)); err != nil {
			t.Fatal(err)
		}
		read, err := readBinary64(&stored)
		if err != nil || uint64(read) != bits {
			t.Fatal("raw storage lost bits", err)
		}
	}
}

func TestKnownBytesAndInvalidFiles(t *testing.T) {
	want := []byte{0x3f, 0xf0, 0, 0, 0, 0, 0, 0}
	got := encode(1).bytes()
	if !bytes.Equal(got[:], want) {
		t.Fatal("wrong byte order")
	}
	for _, size := range []int{0, 1, 7, 9, 100} {
		if _, err := readBinary64(bytes.NewReader(make([]byte, size))); err == nil {
			t.Fatalf("accepted %d bytes", size)
		}
	}
	if err := writeBinary64(shortWriter{}, encode(1)); err != io.ErrShortWrite {
		t.Fatal("short write not detected")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestCLIStorageAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "number.bin")
	if err := run([]string{"-value", "-0", "-write", path}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) != 8 || raw[0] != 0x80 {
		t.Fatal("negative zero storage failed")
	}
	var out bytes.Buffer
	if err := run([]string{"-read", path}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Sign: 1") || !strings.Contains(out.String(), "Class: zero") {
		t.Fatal(out.String())
	}
	if err := run([]string{"-value", "1", "-write", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("existing file overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, raw) {
		t.Fatal("existing file changed")
	}
	for _, args := range [][]string{{"-value", "oops"}, {"-value", "1e999"}, {"-value", "1", "-read", path}, {"extra"}} {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
}
