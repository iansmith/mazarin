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

// queueByteFunc is set during UART driver init to route through the
// device's TX ring buffer (interrupt-driven). Before init, nil → falls
// back to PollWrite.
var queueByteFunc func(byte)

// SetQueueByteFunc registers the interrupt-driven TX path.
// Called during device init with the driver's WriteByte method.
func SetQueueByteFunc(f func(byte)) { queueByteFunc = f }

// QueueByte writes a byte to the TX ring buffer (interrupt-driven).
// Falls back to PollWrite before driver init.
//
//go:nosplit
func QueueByte(b byte) {
	f := queueByteFunc
	if f != nil {
		f(b)
		return
	}
	PollWrite(b) // Early boot fallback
}

// QueueString writes a string to the TX ring buffer (interrupt-driven).
// Falls back to PollWrite before driver init.
//
//go:nosplit
func QueueString(s string) {
	for i := 0; i < len(s); i++ {
		QueueByte(s[i])
	}
}

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
// Handles full uint64 range (up to 20 digits) using a fixed-size buffer
// to stay within nosplit stack limits.
//
//go:nosplit
func RawUARTDecimal(val uint64) {
	if val == 0 {
		PollWrite('0')
		return
	}
	// 20 digits covers the full uint64 range (max 18446744073709551615).
	var buf [20]byte
	n := 0
	for val > 0 {
		buf[n] = byte(val%10) + '0'
		val /= 10
		n++
	}
	// Print in reverse (most significant first)
	for i := n - 1; i >= 0; i-- {
		PollWrite(buf[i])
	}
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
