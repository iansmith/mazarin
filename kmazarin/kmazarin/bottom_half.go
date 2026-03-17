
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
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
//   2. KernelIdleLoop (thread 0) checks flags → sends on channels (wakes bottom halves)
//   3. Bottom half processors (goroutines) wait on channels → process in safe Go context
//
// Why this works:
//   - IRQ handlers are pure assembly (no Go calls)
//   - Flags are just atomic loads/stores (safe from any context)
//   - KernelIdleLoop bridges flags to channels each iteration
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

// ============================================================================
// Other Bottom Half Flags
// ============================================================================

var (
	DeadlinePending uint32 // Flag: deadlines need processing
)

// topHalfIRQNum is set by the assembly IRQ handler before calling NonTimerIRQTopHalf.
var topHalfIRQNum uint64

// topHalfKbd and topHalfMouse hold the pointers needed to read events
// in the nosplit top-half. Set during input device init.
var topHalfKbd topHalfDev
var topHalfMouse topHalfDev

// softIRQRingSize must be a power of 2 for mask-based indexing.
// 256 entries: small enough that the ring empties between processing cycles,
// forcing the consumer to block periodically (releasing the Go runtime P).
// A larger ring causes the consumer to monopolize the P during heavy input.
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
	// Debug counters
	dbgPushOK        uint32     // events successfully pushed to ring
	dbgPushFail      uint32     // events dropped (ring full)
	dbgIRQCount      uint32     // total IRQ invocations
}

var topHalfKbdRing softIRQRing
var topHalfMouseRing softIRQRing
var topHalfUartRing softIRQRing
var topHalfTimerRing softIRQRing

// Debug counters for event flow tracking
var dbgDrainTotal uint32          // total events drained by userspace
var dbgDrainCalls uint32          // total drain syscalls
var dbgDrainPerSlot [maxSoftIRQSlots]uint32 // per-slot drain counts


// uartIRQNum is set during device init so the event poller can wake
// the soft IRQ slot after UART dispatch.
var uartIRQNum uint32

// Block device IRQ state — set by SetBlockIRQ during init.
var blockIRQNum uint32
var blockISRBase uintptr
var blockIOComplete *uint32

// SetBlockIRQ registers the block device's IRQ number, ISR base address,
// and IOComplete flag pointer with the top-half dispatcher.
func SetBlockIRQ(irqNum uint32, isrBase uintptr, ioComplete *uint32) {
	blockIRQNum = irqNum
	blockISRBase = isrBase
	blockIOComplete = ioComplete
}

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

	// Block device: acknowledge interrupt, signal IOComplete for WFI loop
	if irqNum == blockIRQNum && blockIRQNum != 0 {
		if blockISRBase != 0 {
			_ = asm.MmioRead8(blockISRBase) // Acknowledge interrupt (deasserts INTx)
		}
		// DMA read barrier: ensure the device's used ring DMA writes are
		// visible to this CPU before we signal IOComplete. Under HVF the
		// VirtIO backend runs on a separate host thread; without this
		// barrier the IOComplete STLR may become visible to the WFI loop
		// before the used ring data has propagated through the coherency
		// domain. Combined with the DMB in EnableIRQsAndWait, this gives
		// the WFI reader a happens-before on the DMA data.
		asm.DmaRmb()
		if blockIOComplete != nil {
			atomic.StoreUint32(blockIOComplete, 1) // Signal completion
		}
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

	dev.dbgIRQCount++

	// Read ISR to acknowledge interrupt at device level (deasserts PCI INTx).
	if dev.isrBase != 0 {
		_ = asm.MmioRead8(dev.isrBase)
	}

	usedIdx := asm.MmioRead16(dev.usedVA + 2)
	reposted := false

	for dev.lastUsedIdx != usedIdx {
		ringIdx := dev.lastUsedIdx % dev.queueSize
		entryAddr := dev.usedVA + 4 + uintptr(ringIdx)*8
		descIdx := asm.MmioRead32(entryAddr)

		if descIdx < uint32(dev.queueSize) {
			evtAddr := dev.evtBufVA + uintptr(descIdx)*8
			evtType := asm.MmioRead16(evtAddr)
			evtCode := asm.MmioRead16(evtAddr + 2)
			// Read value as two 16-bit halves: Device-nGnRnE mapped DMA pages
			// on ARM64 return 0 for 32-bit reads under QEMU TCG, but 16-bit
			// reads work correctly. VirtIO DMA buffers should be Normal memory
			// but are currently mapped as Device on ARM64 (unlike RISC-V).
			evtValueLo := uint32(asm.MmioRead16(evtAddr + 4))
			evtValueHi := uint32(asm.MmioRead16(evtAddr + 6))
			evtValue := evtValueLo | (evtValueHi << 16)

			ev := hid.HIDEvent{Type: evtType, Code: evtCode, Value: evtValue}

			// Track modifier key state for the constraint attribute system.
			// Only keyboard EV_KEY events can change modifier state.
			if dev == &topHalfKbd && evtType == hid.EvKey {
				ksyscall.TopHalfUpdateModifiers(evtCode, evtValue)
			}

			if !ringPush(dev.ring, ev) {
				dev.dbgPushFail++
				// Ring full — event dropped. Descriptor is ALWAYS reposted
				// below so the device keeps its buffers. Never hold back
				// descriptors: that risks permanent device starvation when
				// all buffers are leaked and no IRQs can fire.
			} else {
				dev.dbgPushOK++
			}

			// ALWAYS repost buffer to device, even if ring push failed.
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
			reposted = true
		}

		dev.lastUsedIdx++
	}

	// Sync VirtQueue.LastUsedIdx so DrainEvents does not re-drain
	// events we already pushed into the softIRQ ring.
	if dev.lastUsedIdxSync != nil {
		*dev.lastUsedIdxSync = dev.lastUsedIdx
	}

	if reposted {
		asm.Dsb()
		asm.MmioWrite16(dev.availVA+2, dev.nextAvailIdx)
		asm.Dsb()
		asm.MmioWrite16(dev.notifyAddr, 0)
		asm.Dsb()
		_ = asm.MmioRead16(dev.notifyAddr)
	}

	if reposted {
		// Wake any thread blocked on a slot for this IRQ.
		// Use 'reposted' (not 'pushed') because even when ring push fails
		// (ring full), the ring already has events that the consumer should
		// drain. If we only woke on 'pushed', a consumer that blocked
		// between batches (ring momentarily empty) and then the ring
		// re-filled with all pushes failing would NEVER be woken.
		WakeSlotForIRQ(irqNum)
	}
}

// breadcrumbHex32 prints a uint32 as hex digits via UART breadcrumbs.
//
//go:nosplit
func breadcrumbHex32(v uint32) {
	serial.RawUARTHex32(v)
}

// breadcrumbDec16 prints a uint16 as decimal digits via UART breadcrumbs.
//
//go:nosplit
func breadcrumbDec16(v uint16) {
	serial.RawUARTDecimal(uint64(v))
}

// ============================================================================
// Event Channels
// ============================================================================

var (
	uartRxEventChan       = make(chan struct{}, 1) // Buffered to avoid blocking poller
	deadlineEventChan     = make(chan struct{}, 1)
	pageTrackingEventChan = make(chan struct{}, 1)
)

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
	serial.PollWrite(b)
}

// BreadcrumbString writes a string as breadcrumbs.
// Safe from any context, but blocks - use for debug only.
//
//go:nosplit
func BreadcrumbString(s string) {
	serial.RawUARTPuts(s)
}

// BreadcrumbHex writes a 64-bit hex value directly to UART.
// TEMPORARY: for kernel memory diagnostics.
//
//go:nosplit
func BreadcrumbHex(val uint64) {
	serial.RawUARTHexCompact(val)
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
	go uartRxBottomHalf()
	go deadlineBottomHalf()
	go pageTrackingBottomHalf()
}
