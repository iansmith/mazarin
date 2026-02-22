
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/shared/hid"
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

// topHalfIRQNum is set by the assembly IRQ handler before calling NonTimerIRQTopHalf.
var topHalfIRQNum uint64

// topHalfKbd and topHalfMouse hold the pointers needed to read events
// in the nosplit top-half. Set during input device init.
var topHalfKbd topHalfDev
var topHalfMouse topHalfDev

// softIRQRingSize must be a power of 2 for mask-based indexing.
const softIRQRingSize = 256

// softIRQRing is an SPSC ring buffer for delivering HID events from
// the nosplit top-half (producer) to the syscall drain path (consumer).
type softIRQRing struct {
	events [softIRQRingSize]hid.HIDEvent
	head   uint32 // atomic: consumer (syscall side)
	tail   uint32 // atomic: producer (top-half)
}

type topHalfDev struct {
	irqNum           uint32
	usedVA           uintptr // VA of VirtQUsed (Device-mapped)
	evtBufVA         uintptr // VA of EventBuffers array (Device-mapped)
	availVA          uintptr // VA of VirtQAvailable (Device-mapped)
	descVA           uintptr // VA of descriptor table (Device-mapped)
	notifyAddr       uintptr // VA of notify register (Device-mapped MMIO)
	evtBufPA         uintptr // PA of EventBuffers (for descriptor addr field)
	isrBase          uintptr // VA of VirtIO ISR register (read to deassert PCI INTx)
	lastUsedIdx      uint16
	queueSize        uint16
	nextAvailIdx     uint16
	ring             *softIRQRing
	lastUsedIdxSync  *uint16 // points to VirtQueue.LastUsedIdx to prevent double-drain
	// Backpressure: when softIRQ ring is full, we consume used entries but
	// don't repost their descriptors. The descriptor indices are saved here
	// so they can be recovered when the ring has space on the next interrupt.
	leakedDescs      [32]uint16 // descriptor indices consumed but not reposted
	leakedCount      uint16     // number of valid entries in leakedDescs
}

var topHalfKbdRing softIRQRing
var topHalfMouseRing softIRQRing
var topHalfUartRing softIRQRing
var topHalfTimerRing softIRQRing

// uartIRQNum is set during device init so the event poller can wake
// the soft IRQ slot after UART dispatch.
var uartIRQNum uint32

// SetTopHalfDev is called during input init to register device pointers
// for the nosplit top-half path.
func SetTopHalfDev(irqNum uint32, usedVA, evtBufVA, availVA, descVA, notifyAddr, evtBufPA, isrBase uintptr, queueSize, initAvailIdx uint16, isMouse bool, lastUsedIdxSync *uint16) {
	dev := &topHalfKbd
	ring := &topHalfKbdRing
	if isMouse {
		dev = &topHalfMouse
		ring = &topHalfMouseRing
	}
	dev.irqNum = irqNum
	dev.usedVA = usedVA
	dev.evtBufVA = evtBufVA
	dev.availVA = availVA
	dev.descVA = descVA
	dev.notifyAddr = notifyAddr
	dev.evtBufPA = evtBufPA
	dev.isrBase = isrBase
	dev.lastUsedIdx = 0
	dev.queueSize = queueSize
	dev.nextAvailIdx = initAvailIdx
	dev.ring = ring
	dev.lastUsedIdxSync = lastUsedIdxSync
}

// ringPush adds one HIDEvent to the ring buffer.
// Returns false if ring is full (overflow — event dropped).
//
//go:nosplit
//go:noinline
func ringPush(r *softIRQRing, ev hid.HIDEvent) bool {
	tail := atomic.LoadUint32(&r.tail)
	head := atomic.LoadUint32(&r.head)
	if tail-head >= softIRQRingSize {
		return false // full
	}
	r.events[tail&(softIRQRingSize-1)] = ev
	atomic.StoreUint32(&r.tail, tail+1)
	return true
}

// RingDrain copies up to max events from the ring into buf.
// Returns the number of events drained.
//
//go:noinline
func RingDrain(r *softIRQRing, buf []hid.HIDEvent, max int) int {
	n := 0
	for n < max {
		head := atomic.LoadUint32(&r.head)
		tail := atomic.LoadUint32(&r.tail)
		if head == tail {
			break
		}
		buf[n] = r.events[head&(softIRQRingSize-1)]
		atomic.StoreUint32(&r.head, head+1)
		n++
	}
	return n
}

// pollInputTopHalf checks both keyboard and mouse VirtIO used rings for
// pending events, drains them into softIRQ ring buffers, reposts descriptors,
// and wakes any blocked userspace slot. Called from eventPoller (normal
// goroutine context). On ARM64 where NonTimerIRQTopHalf already drained the
// rings, lastUsedIdx == usedIdx so this returns immediately.
func pollInputTopHalf() {
	pollOneInputDev(&topHalfKbd)
	pollOneInputDev(&topHalfMouse)
}

// pollOneInputDev drains one VirtIO input device's used ring into the
// associated softIRQ ring buffer. Only active for polled devices (irqNum==0).
// For IRQ-driven devices (ARM64, RISC-V, x86_64 with MSI-X), all VirtIO
// queue mutations happen in NonTimerIRQTopHalf on the exception stack.
// Touching dev.* from goroutine context would race with the ISR.
func pollOneInputDev(dev *topHalfDev) {
	if dev.usedVA == 0 {
		return
	}
	// On ARM64/RISC-V/x86_64-MSI-X the interrupt top-half drains the
	// used ring. Skip polling to avoid racing with the ISR.
	if dev.irqNum != 0 {
		return
	}

	// Sync with DrainEvents to avoid double-processing the same entries.
	if dev.lastUsedIdxSync != nil {
		syncIdx := *dev.lastUsedIdxSync
		if syncIdx != dev.lastUsedIdx {
			dev.lastUsedIdx = syncIdx
		}
	}

	// RISC-V has no Device memory attribute in page tables, so DMA writes
	// from the VirtIO device may not be visible without an explicit fence.
	asm.Dsb()
	usedIdx := asm.MmioRead16(dev.usedVA + 2)

	pushed := false

	for dev.lastUsedIdx != usedIdx {
		ringIdx := dev.lastUsedIdx % dev.queueSize
		entryAddr := dev.usedVA + 4 + uintptr(ringIdx)*8
		descIdx := asm.MmioRead32(entryAddr)

		if descIdx < uint32(dev.queueSize) {
			evtAddr := dev.evtBufVA + uintptr(descIdx)*8
			evtType := asm.MmioRead16(evtAddr)
			evtCode := asm.MmioRead16(evtAddr + 2)
			evtValue := asm.MmioRead32(evtAddr + 4)

			ev := hid.HIDEvent{Type: evtType, Code: evtCode, Value: evtValue}
			ringPush(dev.ring, ev)
			pushed = true

			// Repost buffer to device
			descAddr := dev.descVA + uintptr(descIdx)*16
			bufPA := uint64(dev.evtBufPA) + uint64(descIdx)*8
			asm.MmioWrite32(descAddr, uint32(bufPA))
			asm.MmioWrite32(descAddr+4, uint32(bufPA>>32))
			asm.MmioWrite32(descAddr+8, 8)
			asm.MmioWrite16(descAddr+12, 2)
			asm.MmioWrite16(descAddr+14, 0xFFFF)
			availRingIdx := dev.nextAvailIdx % dev.queueSize
			asm.MmioWrite16(dev.availVA+4+uintptr(availRingIdx)*2, uint16(descIdx))
			dev.nextAvailIdx++
		}

		dev.lastUsedIdx++
	}

	// Sync VirtQueue.LastUsedIdx so DrainEvents does not re-drain
	// events we already pushed into the softIRQ ring.
	if dev.lastUsedIdxSync != nil {
		*dev.lastUsedIdxSync = dev.lastUsedIdx
	}

	if pushed {
		asm.Dsb()
		asm.MmioWrite16(dev.availVA+2, dev.nextAvailIdx)
		asm.Dsb()
		asm.MmioWrite16(dev.notifyAddr, 0)
		asm.Dsb()
		_ = asm.MmioRead16(dev.notifyAddr)

		WakeSlotForIRQ(dev.irqNum)
	}
}

// NonTimerIRQTopHalf is called directly from the assembly exception handler
// (on the exception stack with g set to kmazarin g0) for non-timer IRQs.
// Reads the virtqueue used ring, pushes HIDEvents into the per-device
// softIRQRing, and wakes any blocked slot consumer.
// All functions called must be nosplit and non-allocating.
//
//go:nosplit
//go:noinline
func NonTimerIRQTopHalf() {
	// Read IRQ number from global (set by assembly) and copy to per-CPU
	irqNum := uint32(topHalfIRQNum)
	GetPerCPU().TopHalfIRQNum = uint64(irqNum)

	// UART RX: drain PL011 FIFO directly via MMIO, push bytes to ring
	if irqNum == uartIRQNum && uartIRQNum != 0 {
		uartTopHalf(irqNum)
		return
	}

	var dev *topHalfDev
	if irqNum == topHalfKbd.irqNum && topHalfKbd.usedVA != 0 {
		dev = &topHalfKbd
	} else if irqNum == topHalfMouse.irqNum && topHalfMouse.usedVA != 0 {
		dev = &topHalfMouse
	}
	if dev == nil {
		return
	}

	// Read ISR to acknowledge interrupt at device level.
	if dev.isrBase != 0 {
		_ = asm.MmioRead8(dev.isrBase)
	}

	// Recover leaked descriptors from a previous overflow: if the softIRQ
	// ring now has space, repost the descriptors we held back last time.
	// This runs entirely within the ISR (nosplit) so there's no race.
	recovered := false
	if dev.leakedCount > 0 {
		tail := atomic.LoadUint32(&dev.ring.tail)
		head := atomic.LoadUint32(&dev.ring.head)
		avail := softIRQRingSize - (tail - head)
		if avail > 0 {
			// Ring has space — repost leaked descriptors so the device
			// gets its buffers back.
			for i := uint16(0); i < dev.leakedCount; i++ {
				descIdx := dev.leakedDescs[i]
				descAddr := dev.descVA + uintptr(descIdx)*16
				bufPA := uint64(dev.evtBufPA) + uint64(descIdx)*8
				asm.MmioWrite32(descAddr, uint32(bufPA))
				asm.MmioWrite32(descAddr+4, uint32(bufPA>>32))
				asm.MmioWrite32(descAddr+8, 8)
				asm.MmioWrite16(descAddr+12, 2)
				asm.MmioWrite16(descAddr+14, 0xFFFF)
				availRingIdx := dev.nextAvailIdx % dev.queueSize
				asm.MmioWrite16(dev.availVA+4+uintptr(availRingIdx)*2, uint16(descIdx))
				dev.nextAvailIdx++
			}
			dev.leakedCount = 0
			recovered = true
		}
	}

	usedIdx := asm.MmioRead16(dev.usedVA + 2)
	pushed := false

	for dev.lastUsedIdx != usedIdx {
		ringIdx := dev.lastUsedIdx % dev.queueSize
		entryAddr := dev.usedVA + 4 + uintptr(ringIdx)*8
		descIdx := asm.MmioRead32(entryAddr)

		if descIdx < uint32(dev.queueSize) {
			evtAddr := dev.evtBufVA + uintptr(descIdx)*8
			evtType := asm.MmioRead16(evtAddr)
			evtCode := asm.MmioRead16(evtAddr + 2)
			evtValue := asm.MmioRead32(evtAddr + 4)

			ev := hid.HIDEvent{Type: evtType, Code: evtCode, Value: evtValue}
			if !ringPush(dev.ring, ev) {
				// Ring full — apply backpressure: consume the used entry
				// (advance lastUsedIdx) but don't repost the descriptor.
				// Save the descriptor index so we can recover it later.
				if dev.leakedCount < 32 {
					dev.leakedDescs[dev.leakedCount] = uint16(descIdx)
					dev.leakedCount++
				}
				// Continue draining so lastUsedIdx stays in sync.
				// Events are dropped but the used ring is fully consumed.
				dev.lastUsedIdx++
				continue
			}
			pushed = true

			// Repost buffer to device
			descAddr := dev.descVA + uintptr(descIdx)*16
			bufPA := uint64(dev.evtBufPA) + uint64(descIdx)*8
			asm.MmioWrite32(descAddr, uint32(bufPA))
			asm.MmioWrite32(descAddr+4, uint32(bufPA>>32))
			asm.MmioWrite32(descAddr+8, 8)
			asm.MmioWrite16(descAddr+12, 2)
			asm.MmioWrite16(descAddr+14, 0xFFFF)
			availRingIdx := dev.nextAvailIdx % dev.queueSize
			asm.MmioWrite16(dev.availVA+4+uintptr(availRingIdx)*2, uint16(descIdx))
			dev.nextAvailIdx++
		}

		dev.lastUsedIdx++
	}

	// Sync VirtQueue.LastUsedIdx so DrainEvents (called later by eventPoller)
	// sees nothing to drain. This prevents double-drain and descriptor leak.
	if dev.lastUsedIdxSync != nil {
		*dev.lastUsedIdxSync = dev.lastUsedIdx
	}

	if pushed || recovered {
		asm.Dsb()
		asm.MmioWrite16(dev.availVA+2, dev.nextAvailIdx)
		asm.Dsb()
		asm.MmioWrite16(dev.notifyAddr, 0)
		asm.Dsb()
		_ = asm.MmioRead16(dev.notifyAddr)
	}

	if pushed || recovered {
		// Wake any thread blocked on a slot for this IRQ
		WakeSlotForIRQ(irqNum)
	}
}

// breadcrumbHex32 prints a uint32 as hex digits via UART breadcrumbs.
//
//go:nosplit
func breadcrumbHex32(v uint32) {
	const hex = "0123456789abcdef"
	for i := 28; i >= 0; i -= 4 {
		console.Breadcrumb(hex[(v>>uint(i))&0xf])
	}
}

// breadcrumbDec16 prints a uint16 as decimal digits via UART breadcrumbs.
//
//go:nosplit
func breadcrumbDec16(v uint16) {
	if v == 0 {
		console.Breadcrumb('0')
		return
	}
	// Max uint16 is 65535 = 5 digits
	var buf [5]byte
	n := 0
	for v > 0 {
		buf[n] = byte('0' + v%10)
		v /= 10
		n++
	}
	// Print in reverse (most significant first)
	for n > 0 {
		n--
		console.Breadcrumb(buf[n])
	}
}

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
	console.KPrintf("[eventPoller] started\n")
	for {
		// Yield to other goroutines periodically
		runtime.Gosched()

		// Check generic IRQ pending flags
		// Scan the entire array and call registered handlers.
		// Skip IRQs already handled by NonTimerIRQTopHalf (called from
		// assembly): those events are in softIRQ ring buffers already.
		// Dispatching them again via kirq would call HandleIRQ/DrainEvents,
		// stealing events from the hardware used ring before pollInputTopHalf
		// can deliver them to userspace.
		for irqNum := uint32(0); irqNum < 1020; irqNum++ {
			if atomic.SwapUint32(&irqPendingFlags[irqNum], 0) == 1 {
				if irqNum == topHalfKbd.irqNum || irqNum == topHalfMouse.irqNum || (irqNum == uartIRQNum && uartIRQNum != 0) {
					continue
				}
				dispatchIRQ(irqNum)
			}
		}

		// Poll VirtIO input devices: drain used rings into softIRQ ring
		// buffers and wake blocked slots. On ARM64/RISC-V the IRQ top-half
		// already drained (this is a no-op). On AMD64 with interrupts
		// enabled (irqNum != 0) this is also a no-op.
		pollInputTopHalf()

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

		// Flush any pending console ring data to userspace.
		// Nosplit KWriteByte/KWriteString pushes to the ring but can't
		// call WakeSlotForIRQ. The event poller does it on their behalf.
		if softIRQConsole != nil {
			softIRQConsole.CheckPendingWake()
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
	// Call kirq.DispatchIRQ with dummy values for framePtr, elr, spEl0
	// (these aren't needed for simple handlers like UART)
	kirq.DispatchIRQ(uint64(irqNum), 0, 0, 0)

	// NOTE: EOIR is written in the assembly top-half (exceptions_arm64.s)
	// immediately after setting the pending flag. This ensures the GIC
	// unblocks for the next interrupt without waiting for the bottom-half.
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

// SetupUartSoftIRQ records the UART IRQ number so NonTimerIRQTopHalf
// can recognize it and drain the PL011 FIFO directly.
func SetupUartSoftIRQ(irqNum uint32) {
	uartIRQNum = irqNum
}

// ============================================================================
// Startup
// ============================================================================

// StartBottomHalfProcessors starts all bottom half processor goroutines.
// Must be called during initialization, BEFORE enabling interrupts.
//
func StartBottomHalfProcessors() {
	go eventPoller()
	go uartRxBottomHalf()
	go uartTxBottomHalf()
	go deadlineBottomHalf()
	go pageTrackingBottomHalf()
}
