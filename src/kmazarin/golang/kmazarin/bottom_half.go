//go:build qemuvirt && aarch64

package main

import (
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"
)

// ============================================================================
// Bottom Half Processing - Safe Go Context for Deferred IRQ Work
// ============================================================================
//
// This file implements the "lower half" of interrupt handling using channels.
//
// Architecture:
//   1. IRQ handlers (assembly) drain hardware FIFOs → ring buffers, set flags
//   2. Event poller (goroutine) checks flags → sends on channels (wakes bottom halves)
//   3. Bottom half processors (goroutines) wait on channels → process in safe Go context
//
// Why this works:
//   - IRQ handlers are pure assembly (no Go calls)
//   - Flags are just atomic loads/stores (safe from any context)
//   - Channels properly wake goroutines via runtime.goready()
//   - Bottom half processors run in normal goroutine context (safe for Go code)

// ============================================================================
// UART Ring Buffers and Flags
// ============================================================================

// UART RX: IRQ handler writes, Go reads
const uartRxRingSize = 4096

var (
	uartRxRingBuffer [uartRxRingSize]byte
	uartRxRingHead   uint32 // Read position (Go code)
	uartRxRingTail   uint32 // Write position (IRQ handler)
	uartRxPending    uint32 // Flag: RX data available
)

// UART TX: Go writes, IRQ handler reads
const uartTxRingSize = 4096

var (
	uartTxRingBuffer [uartTxRingSize]byte
	uartTxRingHead   uint32 // Read position (IRQ handler)
	uartTxRingTail   uint32 // Write position (Go code)
	uartTxPending    uint32 // Flag: TX data to send
)

// ============================================================================
// Other Bottom Half Flags
// ============================================================================

var (
	deadlinePending uint32 // Flag: deadlines need processing
)

// ============================================================================
// Event Channels
// ============================================================================

var (
	uartRxEventChan    = make(chan struct{}, 1) // Buffered to avoid blocking poller
	uartTxEventChan    = make(chan struct{}, 1)
	deadlineEventChan  = make(chan struct{}, 1)
)

// ============================================================================
// Event Poller - Bridges Async IRQ World to Sync Go World
// ============================================================================

// eventPoller runs continuously, checking atomic flags set by IRQ handlers
// and sending on channels to wake the appropriate bottom half processor.
//
// This goroutine is the bridge between:
//   - Async world: IRQ handlers set flags (unsafe for Go calls)
//   - Sync world: Channel sends wake goroutines (safe Go context)
//
// Latency: Polls every 10µs, so worst-case wakeup delay is 10µs.
//
func eventPoller() {
	ticker := time.NewTicker(10 * time.Microsecond)
	defer ticker.Stop()

	for range ticker.C {
		// Check UART RX flag
		if atomic.SwapUint32(&uartRxPending, 0) == 1 {
			// Non-blocking send - if channel already has a pending event, skip
			select {
			case uartRxEventChan <- struct{}{}:
			default:
			}
		}

		// Check UART TX flag
		if atomic.SwapUint32(&uartTxPending, 0) == 1 {
			select {
			case uartTxEventChan <- struct{}{}:
			default:
			}
		}

		// Check deadline flag
		if atomic.SwapUint32(&deadlinePending, 0) == 1 {
			select {
			case deadlineEventChan <- struct{}{}:
			default:
			}
		}
	}
}

// ============================================================================
// UART RX Bottom Half
// ============================================================================

// uartRxBottomHalf processes received UART data in safe Go context.
// It blocks on a channel until the event poller signals that data is available.
//
func uartRxBottomHalf() {
	for range uartRxEventChan {
		// Process all available bytes from ring buffer
		processUartRxBuffer()
	}
}

// processUartRxBuffer reads bytes from the RX ring buffer (filled by IRQ handler)
// and processes them. This runs in safe Go context.
//
func processUartRxBuffer() {
	for {
		// Atomically read head and tail
		head := atomic.LoadUint32(&uartRxRingHead)
		tail := atomic.LoadUint32(&uartRxRingTail)

		if head == tail {
			// Ring buffer empty
			return
		}

		// Read byte from buffer
		b := uartRxRingBuffer[head&(uartRxRingSize-1)]

		// Update head atomically
		newHead := (head + 1) & (uartRxRingSize - 1)
		if !atomic.CompareAndSwapUint32(&uartRxRingHead, head, newHead) {
			// Race condition (shouldn't happen), retry
			continue
		}

		// Process the byte in safe Go context
		processRxByte(b)
	}
}

// processRxByte handles a single received byte.
// This is where protocol handling, echoing, etc. would go.
//
//go:nosplit
func processRxByte(b byte) {
	// For now, just echo back
	// In the future, this could build command buffers, parse protocols, etc.
	UartWriteByte(b)
}

// ============================================================================
// UART TX Bottom Half
// ============================================================================

// uartTxBottomHalf handles TX completion events.
// Currently just a placeholder - TX is mostly handled by the IRQ handler
// draining the ring buffer automatically.
//
func uartTxBottomHalf() {
	for range uartTxEventChan {
		// TX IRQ fired - ring buffer is being drained
		// Could check for errors, update statistics, etc.
		// For now, nothing to do
	}
}

// ============================================================================
// Deadline Processing Bottom Half
// ============================================================================

// deadlineBottomHalf processes timer deadlines in safe Go context.
// It blocks on a channel until the timer IRQ handler signals that
// deadlines need to be checked.
//
func deadlineBottomHalf() {
	for range deadlineEventChan {
		// Process deadline queue in safe Go context
		ProcessDeadlines()
	}
}

// ============================================================================
// UART Write Functions (Called from Go Code)
// ============================================================================

// UartWriteByte writes a single byte to the UART TX ring buffer.
// If the buffer is full, it falls back to direct MMIO (blocking).
//
//go:nosplit
func UartWriteByte(b byte) {
	// Try to add to ring buffer
	head := atomic.LoadUint32(&uartTxRingHead)
	tail := atomic.LoadUint32(&uartTxRingTail)
	nextTail := (tail + 1) & (uartTxRingSize - 1)

	if nextTail == head {
		// Buffer full - fall back to direct MMIO
		uartPutcDirect(b)
		return
	}

	// Add to ring buffer
	uartTxRingBuffer[tail] = b
	atomic.StoreUint32(&uartTxRingTail, nextTail)

	// Enable TX interrupt to drain buffer
	enableUartTxInterrupt()
}

// UartWrite writes multiple bytes to the UART TX ring buffer.
// Returns the number of bytes written.
//
func UartWrite(data []byte) int {
	written := 0

	for _, b := range data {
		head := atomic.LoadUint32(&uartTxRingHead)
		tail := atomic.LoadUint32(&uartTxRingTail)
		nextTail := (tail + 1) & (uartTxRingSize - 1)

		if nextTail == head {
			// Buffer full - can't write more
			break
		}

		uartTxRingBuffer[tail] = b
		atomic.StoreUint32(&uartTxRingTail, nextTail)
		written++
	}

	if written > 0 {
		enableUartTxInterrupt()
	}

	return written
}

// uartPutcDirect writes directly to UART hardware (blocking).
// Used as fallback when ring buffer is full.
//
//go:nosplit
func uartPutcDirect(b byte) {
	uartBase := GetUartBase()
	// Wait for TX FIFO to have space (bit 5 = TXFF)
	for (*(*uint32)(unsafe.Pointer(uartBase + 0x18)) & 0x20) != 0 {
		// Busy wait
	}
	// Write byte
	*(*uint32)(unsafe.Pointer(uartBase + 0x00)) = uint32(b)
}

// enableUartTxInterrupt enables the UART TX interrupt.
// This is implemented in assembly to safely access UART registers.
//
func enableUartTxInterrupt()

// ============================================================================
// Startup
// ============================================================================

// StartBottomHalfProcessors starts all bottom half processor goroutines.
// Must be called during initialization, BEFORE enabling interrupts.
//
func StartBottomHalfProcessors() {
	Print("[BottomHalf] Starting event poller and processors...")

	// Start event poller
	go eventPoller()

	// Start bottom half processors
	go uartRxBottomHalf()
	go uartTxBottomHalf()
	go deadlineBottomHalf()

	// Give goroutines a moment to start
	runtime.Gosched()

	Print("[BottomHalf] All processors started")
}
