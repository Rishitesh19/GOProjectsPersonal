# Day 4: Store a float64 from scratch

Convert a Go `float64` into IEEE 754 binary64 fields, pack them into eight bytes, save them to a file, and decode them again. Standard library only; Go 1.24+.

The encoder uses arithmetic to extract the sign, exponent, and fraction. The byte packing uses shifts. Production code does not use `math.Float64bits`, `math.Float64frombits`, `encoding/binary`, or `unsafe`; tests use Go's bit conversions as an independent reference.

## Run

From the repository root:

```sh
cd Day4

# Inspect the default value, 0.1:
go run .

# Store exactly eight bytes in a new file:
go run . -value 3.141592653589793 -write number.bin

# Read those bytes back:
go run . -read number.bin

# Try special values and the smallest positive subnormal:
go run . -value=-0
go run . -value=+Inf
go run . -value=NaN
go run . -value=5e-324
```

`-write` refuses to overwrite an existing file. Choose another name for subsequent saves. Files are created with owner-only permissions. `*.bin` files in Day4 are ignored by Git. The program does not use the network or leave a process running.

`-read` requires exactly eight bytes. It can be combined with `-write` to copy the raw representation to a new file, including any NaN payload bits. `-read` and an explicitly supplied `-value` are mutually exclusive.

## The storage layout

```text
63          62                  52 51                              0
+-----------+----------------------+--------------------------------+
| sign: 1   | stored exponent: 11  | fraction: 52                   |
+-----------+----------------------+--------------------------------+
```

The file is **big-endian**: the byte containing the sign and highest exponent bits comes first. There is no text, header, or length field—only the eight bytes for one number.

For normal numbers:

```text
value = (-1)^sign × (1 + fraction / 2^52) × 2^(stored exponent - 1023)
```

The leading `1` is implicit, giving normal values 53 bits of significand precision. Special exponent fields have different meanings:

| Exponent | Fraction | Meaning |
| --- | --- | --- |
| 0 | 0 | Positive or negative zero |
| 0 | Nonzero | Subnormal: `(-1)^sign × fraction × 2^-1074` |
| 1–2046 | Any | Normal number |
| 2047 | 0 | Positive or negative infinity |
| 2047 | Nonzero | NaN |

The encoder repeatedly scales a finite nonzero input by two until it lies in `[1, 2)`. It then constructs the appropriate exponent and fraction. The decoder reconstructs the numeric value through arithmetic. Signed zero is preserved.

For `0.1`, the stored bytes are:

```text
3f b9 99 99 99 99 99 9a
```

The displayed value is `0.10000000000000001` at 17 significant decimal digits because decimal 0.1 is not exactly representable in binary64. Storage preserves the binary64 value; it does not add decimal precision.

## What “from scratch” means here

We implement field extraction, bit packing, byte order, and reconstruction. Go still provides the underlying floating-point arithmetic, and `strconv.ParseFloat` handles decimal text conversion and rounding into a Go `float64`. We are not implementing arbitrary-precision decimal parsing or software floating-point arithmetic.

`math` is used only to identify or construct infinities and construct NaNs. Encoding a NaN canonicalizes it to `0x7ff8000000000000`; its original sign/payload are not preserved through arithmetic conversion. Raw file reads and copies do preserve them. The format is not encrypted and has no checksum.

## Files and tests

- `binary64.go`: arithmetic conversion, field inspection, and eight-byte I/O.
- `main.go`: CLI, decimal parsing, display, and exclusive file creation.
- `binary64_test.go`: reference comparisons and storage tests.

```sh
go test ./...
go vet ./...
```

Tests cover 10,000 seeded random bit patterns plus boundaries: both zeros, smallest subnormal, largest subnormal, smallest normal, largest finite values, infinities, and NaNs. They also verify byte order, exact-size reads, short-write errors, invalid CLI inputs, and refusal to overwrite files.
