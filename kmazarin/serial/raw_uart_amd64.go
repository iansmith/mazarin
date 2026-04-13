//go:build amd64

package serial

import "sync/atomic"

// COM1 I/O ports
// THR (Transmitter Holding Register) = 0x3F8
// LSR (Line Status Register) = 0x3FD

// RawUART writes a single byte to COM1 via OUTB to port 0x3F8.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func RawUART(b byte)

// txReady returns true when COM1's transmitter holding register is empty.
// Reads LSR (port 0x3FD) bit 5 (THRE).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func txReady() bool

// PollWrite polls until TX ready, then writes the byte.
//
//go:nosplit
func PollWrite(b byte) {
	for !txReady() {
	}
	RawUART(b)
}

// RxReady returns true when COM1 has received data.
// Reads LSR (port 0x3FD) bit 0 (Data Ready).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func RxReady() bool

// ReadRxByte reads one byte from COM1's receive buffer (port 0x3F8).
// Caller must ensure RxReady() returned true first.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func ReadRxByte() byte

// EnableRxInterrupt enables the Received Data Available interrupt on COM1
// by writing 0x01 to the IER register (port 0x3F9). After this, COM1 will
// assert its IRQ line (ISA IRQ 4 → IOAPIC input 4) when data arrives.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func EnableRxInterrupt()

// EnableTxInterrupt enables RX+TX interrupts on COM1 (IER = 0x03).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func EnableTxInterrupt()

// DisableTxInterrupt disables TX interrupt, keeping RX enabled (IER = 0x01).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func DisableTxInterrupt()

// COM1 TX ring buffer — interrupt-driven transmit path for x86_64.
// COM1 uses I/O port instructions (not MMIO), so the ring buffer and
// drain logic live here in the serial package rather than in uart/*.
const com1TxBufSize = 4096

var (
	com1TxBuf  [com1TxBufSize]byte
	com1TxHead int    // read position (drain in top-half)
	com1TxTail int    // write position (COM1QueueByte caller)
	com1TxLock uint32 // CAS spinlock
)

// COM1QueueByte pushes a byte to the COM1 TX ring buffer and immediately
// drains as many bytes as possible to COM1 THR. If the THR is busy,
// remaining bytes stay in the ring buffer for the TX interrupt top-half
// to drain later. If both THR and ring buffer are full, the byte is
// silently dropped (non-blocking policy).
//
//go:nosplit
func COM1QueueByte(b byte) {
	// Acquire spinlock
	for !com1TxTryLock() {
	}
	next := (com1TxTail + 1) % com1TxBufSize
	if next == com1TxHead {
		// Buffer full — drop byte
		com1TxUnlock()
		return
	}
	com1TxBuf[com1TxTail] = b
	com1TxTail = next
	// Drain as much as possible to hardware right now.
	// This is critical for COM1/NS16550 whose TX interrupt is edge-triggered
	// (fires on THR transition to empty). Writing to THR creates the
	// transition needed for subsequent TX interrupts.
	for com1TxHead != com1TxTail && txReady() {
		RawUART(com1TxBuf[com1TxHead])
		com1TxHead = (com1TxHead + 1) % com1TxBufSize
	}
	// Enable TX interrupt for any bytes that didn't fit
	if com1TxHead != com1TxTail {
		EnableTxInterrupt()
	}
	com1TxUnlock()
}

// COM1QueueByteTry pushes a byte to the COM1 TX ring buffer, returning false
// if the ring is full. Same as COM1QueueByte but non-blocking on ring-full.
// Used by SyscallWrite to detect short writes.
//
//go:nosplit
func COM1QueueByteTry(b byte) bool {
	for !com1TxTryLock() {
	}
	next := (com1TxTail + 1) % com1TxBufSize
	if next == com1TxHead {
		com1TxUnlock()
		return false
	}
	com1TxBuf[com1TxTail] = b
	com1TxTail = next
	for com1TxHead != com1TxTail && txReady() {
		RawUART(com1TxBuf[com1TxHead])
		com1TxHead = (com1TxHead + 1) % com1TxBufSize
	}
	if com1TxHead != com1TxTail {
		EnableTxInterrupt()
	}
	com1TxUnlock()
	return true
}

// COM1DrainTx drains the TX ring buffer to COM1 THR while THRE is set.
// Called from the top-half when a TX interrupt fires.
// Disables TX interrupt when buffer is empty.
//
//go:nosplit
func COM1DrainTx() {
	if !com1TxTryLock() {
		return // Lock held by writer, next interrupt will drain
	}
	for com1TxHead != com1TxTail && txReady() {
		RawUART(com1TxBuf[com1TxHead])
		com1TxHead = (com1TxHead + 1) % com1TxBufSize
	}
	if com1TxHead == com1TxTail {
		DisableTxInterrupt()
	}
	com1TxUnlock()
}

//go:nosplit
func com1TxTryLock() bool {
	return atomic.CompareAndSwapUint32(&com1TxLock, 0, 1)
}

//go:nosplit
func com1TxUnlock() {
	atomic.StoreUint32(&com1TxLock, 0)
}
