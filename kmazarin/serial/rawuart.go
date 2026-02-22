package serial

// rawuart.go — Shared formatting functions for raw UART output.
//
// SAFETY: All functions are nosplit, IRQ-safe, and allocate nothing.
// They call PollWrite() which polls the hardware TX-ready bit and writes
// bytes directly. Safe to call from: page fault handlers, IRQ handlers,
// normal Go code. Output may interleave on multi-CPU systems (not an
// issue on single-CPU).
//
// For locked/ordered output in normal Go code, use console.KWrite* instead.

const hexChars = "0123456789ABCDEF"

// RawUARTPuts writes a string to UART, polling for TX ready per byte.
//
//go:nosplit
func RawUARTPuts(s string) {
	for i := 0; i < len(s); i++ {
		PollWrite(s[i])
	}
}

// RawUARTHex64 writes a 64-bit value as 16 uppercase hex digits.
//
//go:nosplit
func RawUARTHex64(val uint64) {
	for i := 60; i >= 0; i -= 4 {
		PollWrite(hexChars[(val>>uint(i))&0xF])
	}
}

// RawUARTHex32 writes a 32-bit value as 8 uppercase hex digits.
//
//go:nosplit
func RawUARTHex32(val uint32) {
	for i := 28; i >= 0; i -= 4 {
		PollWrite(hexChars[(val>>uint(i))&0xF])
	}
}

// RawUARTHex16 writes a 16-bit value as 4 uppercase hex digits.
//
//go:nosplit
func RawUARTHex16(val uint16) {
	PollWrite(hexChars[(val>>12)&0xF])
	PollWrite(hexChars[(val>>8)&0xF])
	PollWrite(hexChars[(val>>4)&0xF])
	PollWrite(hexChars[val&0xF])
}

// RawUARTHex8 writes a byte as 2 uppercase hex digits.
//
//go:nosplit
func RawUARTHex8(val uint8) {
	PollWrite(hexChars[val>>4])
	PollWrite(hexChars[val&0xF])
}

// RawUARTDecimal writes a uint64 as decimal digits (no leading zeros).
// Uses fixed individual variables to minimize nosplit stack usage.
//
//go:nosplit
func RawUARTDecimal(val uint64) {
	if val == 0 {
		PollWrite('0')
		return
	}
	// Extract digits in reverse using fixed slots.
	// 5 slots handles 0–99999; larger values truncate (sufficient for debug output).
	var d0, d1, d2, d3, d4 byte
	n := 0
	d0 = byte(val%10) + '0'
	val /= 10
	n = 1
	if val > 0 {
		d1 = byte(val%10) + '0'
		val /= 10
		n = 2
	}
	if val > 0 {
		d2 = byte(val%10) + '0'
		val /= 10
		n = 3
	}
	if val > 0 {
		d3 = byte(val%10) + '0'
		val /= 10
		n = 4
	}
	if val > 0 {
		d4 = byte(val%10) + '0'
		n = 5
	}
	if n >= 5 {
		PollWrite(d4)
	}
	if n >= 4 {
		PollWrite(d3)
	}
	if n >= 3 {
		PollWrite(d2)
	}
	if n >= 2 {
		PollWrite(d1)
	}
	PollWrite(d0)
}

// RawUARTHexCompact writes a hex value with no leading zeros (at least 1 digit).
//
//go:nosplit
func RawUARTHexCompact(val uint64) {
	if val == 0 {
		PollWrite('0')
		return
	}
	// Find the highest non-zero nibble.
	started := false
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> uint(i)) & 0xF
		if nibble != 0 {
			started = true
		}
		if started {
			PollWrite(hexChars[nibble])
		}
	}
}
