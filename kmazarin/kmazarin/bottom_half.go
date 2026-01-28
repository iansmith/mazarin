
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"runtime"
	"sync/atomic"
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
	DeadlinePending uint32 // Flag: deadlines need processing
)

// ============================================================================
// Generic IRQ Pending Flags (for bottom-half dispatch)
// ============================================================================

// irqPendingFlags is an array of flags, one per IRQ number (0-1019).
// The assembly IRQ handler sets irqPendingFlags[irqNum] = 1 when an IRQ fires.
// The event poller checks these flags and dispatches to bottom-half handlers.
//
// This allows us to avoid calling Go code from IRQ context (unsafe on exception stack).
var irqPendingFlags [1020]uint32

// IAR values for each IRQ (used to write GICC_EOIR after handling)
// Set by assembly IRQ handler alongside irqPendingFlags
var irqIARValues [1020]uint32

// ============================================================================
// Event Channels
// ============================================================================

var (
	uartRxEventChan       = make(chan struct{}, 1) // Buffered to avoid blocking poller
	uartTxEventChan       = make(chan struct{}, 1)
	deadlineEventChan     = make(chan struct{}, 1)
	pageTrackingEventChan = make(chan struct{}, 1)
)

// ============================================================================
// Event Poller - Bridges Async IRQ World to Sync Go World
// ============================================================================

// eventPoller runs continuously, checking atomic flags set by IRQ handlers
// and dispatching to registered Go handlers in safe context.
//
// This goroutine is the bridge between:
//   - Async world: IRQ handlers set flags (unsafe for Go calls)
//   - Sync world: Call Go handlers in safe goroutine context
//
// NOTE: Using busy-loop instead of time.Ticker to avoid timer initialization issues.
//
func eventPoller() {
	for {
		// Yield to other goroutines periodically
		runtime.Gosched()

		// Check generic IRQ pending flags
		// Scan the entire array and call registered handlers
		for irqNum := uint32(0); irqNum < 1020; irqNum++ {
			if atomic.SwapUint32(&irqPendingFlags[irqNum], 0) == 1 {
				// IRQ fired - call registered handler in safe Go context
				dispatchIRQ(irqNum)
			}
		}

		// Check UART RX flag (legacy - will be replaced by generic dispatch)
		if atomic.SwapUint32(&uartRxPending, 0) == 1 {
			// Non-blocking send - if channel already has a pending event, skip
			select {
			case uartRxEventChan <- struct{}{}:
			default:
			}
		}

		// Check UART TX flag (legacy - will be replaced by generic dispatch)
		if atomic.SwapUint32(&uartTxPending, 0) == 1 {
			select {
			case uartTxEventChan <- struct{}{}:
			default:
			}
		}

		// Check deadline flag
		if atomic.SwapUint32(&DeadlinePending, 0) == 1 {
			select {
			case deadlineEventChan <- struct{}{}:
			default:
			}
		}

		// Check page tracking flag
		if atomic.SwapUint32(&kmem.PageTrackingPending, 0) == 1 {
			select {
			case pageTrackingEventChan <- struct{}{}:
			default:
			}
		}
	}
}

// writeGICCEOIR writes to the GIC End Of Interrupt Register to acknowledge
// that an interrupt has been fully serviced.
//
//go:nosplit
func writeGICCEOIR(iarValue uint32) {
	// GICC_EOIR = GIC CPU interface base + 0x10
	// Physical: 0x08010000 → High-memory: 0xFFFFFFFF08010000
	const GICC_BASE_PHYS = 0x08010000
	const KERNEL_MMIO_OFFSET = 0xFFFFFFFF00000000
	const GICC_EOIR_OFFSET = 0x10
	addr := uintptr(GICC_BASE_PHYS + KERNEL_MMIO_OFFSET + GICC_EOIR_OFFSET)
	asm.MmioWrite32(addr, iarValue)
}

// dispatchIRQ calls the registered handler for the given IRQ number.
// This runs in safe Go context (event poller goroutine).
//
func dispatchIRQ(irqNum uint32) {
	// Read IAR value saved by exception handler
	iarValue := atomic.LoadUint32(&irqIARValues[irqNum])

	// Call kirq.DispatchIRQ with dummy values for framePtr, elr, spEl0
	// (these aren't needed for simple handlers like UART)
	kirq.DispatchIRQ(uint64(irqNum), 0, 0, 0)

	// CRITICAL: Write GICC_EOIR AFTER handler completes
	// For level-triggered interrupts (like UART TX), the handler must clear
	// the interrupt condition (e.g., drain TX buffer, disable TX interrupt)
	// BEFORE we write EOIR. Otherwise, the GIC immediately re-fires the IRQ.
	writeGICCEOIR(iarValue)
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
// Runs in normal goroutine context, safe to call console functions.
//
func processRxByte(b byte) {
	// For now, just echo back using console abstraction
	// In the future, this could build command buffers, parse protocols, etc.
	console.KWriteByte(b)
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
// Page Tracking Bottom Half
// ============================================================================

// pageTrackingBottomHalf drains the deferred page record queue and
// inserts entries into the page tracker. Runs in normal Go context.
func pageTrackingBottomHalf() {
	for range pageTrackingEventChan {
		kmem.ProcessDeferredRecords()
	}
}

// ============================================================================
// Breadcrumb Debug Output (Safe from ANY context, including IRQ handlers)
// ============================================================================

// Breadcrumb writes a single byte directly to UART hardware.
// This bypasses all abstractions and is safe to call from:
//   - IRQ handlers (exception stack)
//   - Any context where Print() might deadlock
//   - Early boot before console is initialized
//
// Use sparingly for critical debug output only.
//
//go:nosplit
func Breadcrumb(b byte) {
	console.Breadcrumb(b)
}

// BreadcrumbString writes a string as breadcrumbs.
// Safe from any context, but blocks - use for debug only.
//
//go:nosplit
func BreadcrumbString(s string) {
	for i := 0; i < len(s); i++ {
		console.Breadcrumb(s[i])
	}
}

// ============================================================================
// Startup
// ============================================================================

// StartBottomHalfProcessors starts all bottom half processor goroutines.
// Must be called during initialization, BEFORE enabling interrupts.
//
func StartBottomHalfProcessors() {
	Print("[BottomHalf] Starting event poller and processors...")

	Print("[BottomHalf] Starting event poller...")
	// Start event poller
	go eventPoller()
	Print("[BottomHalf] Event poller started")

	Print("[BottomHalf] Starting RX processor...")
	// Start bottom half processors
	go uartRxBottomHalf()
	Print("[BottomHalf] Starting TX processor...")
	go uartTxBottomHalf()
	Print("[BottomHalf] Starting deadline processor...")
	go deadlineBottomHalf()
	Print("[BottomHalf] Starting page tracking processor...")
	go pageTrackingBottomHalf()
	Print("[BottomHalf] All goroutines launched")

	Print("[BottomHalf] About to finish...")

	Print("[BottomHalf] Finished!")
}
