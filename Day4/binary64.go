package main

import (
	"fmt"
	"io"
	"math"
)

const (
	fractionMask uint64 = 1<<52 - 1
	signMask     uint64 = 1 << 63
	canonicalNaN uint64 = 0x7ff8000000000000
)

type binary64 uint64

// encode extracts the IEEE 754 fields using arithmetic. Multiplication and
// division by two are exact here: the input is already a Go binary64 value.
// NaNs are canonicalized because arithmetic does not expose their payloads.
func encode(x float64) binary64 {
	if x != x {
		return binary64(canonicalNaN)
	}
	var sign uint64
	if x < 0 || (x == 0 && 1/x < 0) {
		sign = signMask
		x = -x
	}
	if x == 0 {
		return binary64(sign)
	}
	if math.IsInf(x, 1) {
		return binary64(sign | 0x7ff0000000000000)
	}
	exponent := 0
	for x >= 2 {
		x /= 2
		exponent++
	}
	for x < 1 {
		x *= 2
		exponent--
	}
	if exponent < -1022 {
		// Subnormals have no implicit leading 1; their unit is 2^-1074.
		fraction := uint64(x * float64(uint64(1)<<uint(exponent+1074)))
		return binary64(sign | fraction)
	}
	fraction := uint64((x - 1) * float64(uint64(1)<<52))
	return binary64(sign | uint64(exponent+1023)<<52 | fraction)
}

func (b binary64) fields() (uint64, uint64, uint64) {
	bits := uint64(b)
	return bits >> 63, (bits >> 52) & 0x7ff, bits & fractionMask
}
func (b binary64) class() string {
	_, exponent, fraction := b.fields()
	switch exponent {
	case 0:
		if fraction == 0 {
			return "zero"
		}
		return "subnormal"
	case 0x7ff:
		if fraction == 0 {
			return "infinity"
		}
		return "NaN"
	default:
		return "normal"
	}
}

func (b binary64) decode() float64 {
	sign, exponent, fraction := b.fields()
	if exponent == 0x7ff {
		if fraction != 0 {
			return math.NaN()
		}
		if sign == 1 {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	var x float64
	var power int
	if exponent == 0 {
		x = float64(fraction)
		power = -1074
	} else {
		x = 1 + float64(fraction)/float64(uint64(1)<<52)
		power = int(exponent) - 1023
	}
	for power > 0 {
		x *= 2
		power--
	}
	for power < 0 {
		x /= 2
		power++
	}
	if sign == 1 {
		x = -x
	}
	return x
}

// bytes uses a fixed, portable big-endian file format, independent of the CPU.
func (b binary64) bytes() [8]byte {
	var result [8]byte
	for i := range result {
		result[i] = byte(uint64(b) >> uint(56-8*i))
	}
	return result
}
func readBinary64(r io.Reader) (binary64, error) {
	data, err := io.ReadAll(io.LimitReader(r, 9))
	if err != nil {
		return 0, err
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("expected exactly 8 bytes, got %d%s", len(data), func() string {
			if len(data) == 9 {
				return " or more"
			}
			return ""
		}())
	}
	var bits uint64
	for _, b := range data {
		bits = bits<<8 | uint64(b)
	}
	return binary64(bits), nil
}
func writeBinary64(w io.Writer, b binary64) error {
	data := b.bytes()
	n, err := w.Write(data[:])
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
