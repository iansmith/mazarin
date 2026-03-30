
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
	"sync/atomic"
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

// topHalfKbd, topHalfMouse, and topHalfTablet hold the pointers needed to
// read events in the nosplit top-half. Set during input device init.
var topHalfKbd topHalfDev
var topHalfMouse topHalfDev
var topHalfTablet topHalfDev

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
var topHalfTabletRing softIRQRing
var topHalfUartRing softIRQRing
var topHalfTimerRing softIRQRing
var topHalfBlockRing softIRQRing

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

// Block async completion state (Phase 4).
// When blockAsyncMode=1, the top-half drains the Engine used ring,
// pushes completion events to topHalfBlockRing, and wakes the slot.
// When blockAsyncMode=0 (default), the top-half just sets IOComplete
// for the synchronous WFI loop in blockReadBatch.
var blockEnginePtr uintptr // *virtio.Engine stored as uintptr (nosplit-safe)
var blockAsyncMode uint32  // atomic: 0=sync, 1=async
var blockSidecarFreePtr *uint64 // pointer to SidecarPool.FreeBits for release

// blockAsyncSlot tracks per-IOTag metadata for async completions.
// Populated by SysBlockSubmit, consumed by the IRQ top-half.
//
// IMPORTANT: This struct must NOT contain Go pointer types (*T).
// It is zeroed in the nosplit IRQ top-half chain. A pointer field would
// cause the compiler to emit wbZero (write barrier), which exceeds the
// 792-byte nosplit stack limit. The DMAClump address is stored as uintptr
// to avoid GC write barriers. This is safe because the DMAClump lives in
// the shepherd's fixed DMAClumps array and outlives all in-flight I/O
// (InFlight > 0 prevents deallocation).
type blockAsyncSlot struct {
	sidecarStatusVA uintptr // VA of status byte in sidecar
	sidecarIdx      uint8   // Sidecar slot index (for release)
	dataKernelVA    uintptr // Kernel VA of data page (PA + KernelMMIOOffset)
	dataLen         uint32  // Data buffer size (for cache invalidate)
	clumpAddr       uintptr // VA of *proc.DMAClump, stored as uintptr (no write barrier)
}

var blockAsyncSlots [256]blockAsyncSlot // indexed by IOTag (descriptor head index)

// SetBlockIRQ registers the block device's IRQ number, ISR base address,
// IOComplete flag pointer, engine pointer, and sidecar pool free bits pointer
// with the top-half dispatcher.
func SetBlockIRQ(irqNum uint32, isrBase uintptr, ioComplete *uint32, enginePtr uintptr, sidecarFreePtr *uint64) {
	blockIRQNum = irqNum
	blockISRBase = isrBase
	blockIOComplete = ioComplete
	blockEnginePtr = enginePtr
	blockSidecarFreePtr = sidecarFreePtr
}

// SetBlockAsyncSlot stores per-tag metadata for async completion.
// Called from SysBlockSubmit after engine.Submit returns the IOTag.
// clumpAddr is the VA of the *proc.DMAClump stored as uintptr (0 if no clump).
//
//go:nosplit
func SetBlockAsyncSlot(tag uint16, sidecarStatusVA uintptr, sidecarIdx uint8, dataKernelVA uintptr, dataLen uint32, clumpAddr uintptr) {
	if tag < 256 {
		blockAsyncSlots[tag] = blockAsyncSlot{
			sidecarStatusVA: sidecarStatusVA,
			sidecarIdx:      sidecarIdx,
			dataKernelVA:    dataKernelVA,
			dataLen:         dataLen,
			clumpAddr:       clumpAddr,
		}
	}
}

// EnableBlockAsyncMode switches the top-half to async completion delivery.
// Once enabled, SyscallBlockRead's interrupt path must not be used (falls back to polling).
func EnableBlockAsyncMode() {
	atomic.StoreUint32(&blockAsyncMode, 1)
}

// SetBlockAsyncMode atomically sets the blockAsyncMode flag and returns the previous value.
// The sync polling path uses this to temporarily disable async mode so the top-half
// just sets IOComplete instead of draining the used ring.
func SetBlockAsyncMode(mode uint32) uint32 {
	return atomic.SwapUint32(&blockAsyncMode, mode)
}

// SetTopHalfDev is called during input init to register device pointers
// for the nosplit top-half path.
// devType: 0=keyboard, 1=mouse, 2=tablet
func SetTopHalfDev(irqNum uint32, usedVA, evtBufVA, availVA, descVA, notifyAddr, evtBufPA, isrBase uintptr, queueSize, initAvailIdx uint16, devType int, lastUsedIdxSync *uint16) {
	dev := &topHalfKbd
	ring := &topHalfKbdRing
	if devType == 1 {
		dev = &topHalfMouse
		ring = &topHalfMouseRing
	} else if devType == 2 {
		dev = &topHalfTablet
		ring = &topHalfTabletRing
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

// classifyInputEvent maps an HID event to an InputClass for focus routing.
// dev == &topHalfKbd means keyboard device; nil or other means mouse/tablet.
// Returns -1 if the event doesn't map to any input class (e.g. EV_SYN).
//
//go:nosplit
func classifyInputEvent(dev *topHalfDev, evtType, evtCode uint16) int {
	switch evtType {
	case hid.EvKey:
		if evtCode < hid.BtnMouse {
			return hid.InputClassKeyboard
		}
		return hid.InputClassMouseClick
	case hid.EvRel, hid.EvAbs:
		return hid.InputClassMouseMove
	}
	return -1 // EV_SYN or unknown — don't route
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

	// Block device: acknowledge interrupt, then either drain async completions
	// or signal IOComplete for the synchronous WFI loop.
	if irqNum == blockIRQNum && blockIRQNum != 0 {
		if blockISRBase != 0 {
			_ = asm.MmioRead8(blockISRBase) // Acknowledge interrupt (deasserts INTx)
		}
		// DMA read barrier: ensure the device's used ring DMA writes are
		// visible to this CPU before we access used ring entries or signal
		// IOComplete. Under HVF the VirtIO backend runs on a separate host
		// thread; without this barrier the CPU may see stale used ring data.
		asm.DmaRmb()

		if atomic.LoadUint32(&blockAsyncMode) != 0 && blockEnginePtr != 0 {
			// Async mode: drain engine used ring, push completion events
			eng := (*virtio.Engine)(unsafe.Pointer(blockEnginePtr))
			for eng.HasUsed() {
				info := eng.PopUsed()
				if info.Tag == virtio.InvalidIOTag {
					break
				}
				tag := uint16(info.Tag)
				meta := &blockAsyncSlots[tag]

				// Read status from sidecar (Device-nGnRnE, no cache mgmt needed)
				status := uint16(0)
				if meta.sidecarStatusVA != 0 {
					status = uint16(*(*uint8)(unsafe.Pointer(meta.sidecarStatusVA)))
				}

				// Invalidate cache on data page so userspace sees device-written data
				if meta.dataKernelVA != 0 && meta.dataLen > 0 {
					asm.InvalidateDCacheRange(meta.dataKernelVA, uintptr(meta.dataLen))
				}

				// Release sidecar slot (bitmap OR — nosplit safe)
				if blockSidecarFreePtr != nil {
					*blockSidecarFreePtr |= uint64(1) << uint(meta.sidecarIdx)
				}

				// Decrement InFlight on DMA clump (if applicable).
				// If the clump is pending release and InFlight hits 0,
				// free the contiguous pages. This handles the case where
				// munmap was called while I/O was in flight.
				if meta.clumpAddr != 0 {
					clump := (*proc.DMAClump)(unsafe.Pointer(meta.clumpAddr))
					remaining := atomic.AddInt32(&clump.InFlight, -1)
					if remaining == 0 && (clump.ShepherdDead || clump.PendingRelease) {
						kmem.BuddyFreeTyped(clump.StartPA, clump.BuddyOrder, kmem.PageUserDMA)
					}
				}

				// Push completion event to ring (must use ringPush which
				// uses monotonically-increasing tail — NOT wrapping indices —
				// to match RingDrain's head convention)
				ringPush(&topHalfBlockRing, hid.HIDEvent{
					Type:  tag,
					Code:  status,
					Value: info.UsedLen,
				})

				// Clear metadata slot
				*meta = blockAsyncSlot{}
			}
			WakeSlotForIRQ(hid.BlockVirtualIRQ)
		} else {
			// Sync mode: just signal IOComplete for WFI loop
			if blockIOComplete != nil {
				atomic.StoreUint32(blockIOComplete, 1)
			}
		}
		return
	}

	// Tablet device: drain events, coalesce position, move hardware cursor
	if irqNum == topHalfTablet.irqNum && topHalfTablet.usedVA != 0 {
		topHalfTabletHandler()
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

			// Route through focus-based input system (dual delivery to WM + focused shepherd).
			inputClass := classifyInputEvent(dev, evtType, evtCode)
			if inputClass >= 0 {
				routeInputEvent(ev, inputClass)
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

		// Also wake input focus consumers. Wake all three classes since
		// we may have pushed events of different classes in this batch.
		if dev == &topHalfKbd {
			wakeInputConsumers(hid.InputClassKeyboard)
		} else {
			wakeInputConsumers(hid.InputClassMouseClick)
			wakeInputConsumers(hid.InputClassMouseMove)
		}
	}
}

// topHalfTabletHandler drains the tablet virtqueue, coalesces EV_ABS
// events to get the latest cursor position, pushes events to the tablet
// ring for userspace, and moves the hardware cursor via the GPU cursorq.
// All operations are nosplit-safe.
//
//go:nosplit
//go:noinline
func topHalfTabletHandler() {
	dev := &topHalfTablet
	dev.dbgIRQCount++

	// Read ISR to acknowledge interrupt
	if dev.isrBase != 0 {
		_ = asm.MmioRead8(dev.isrBase)
	}

	usedIdx := asm.MmioRead16(dev.usedVA + 2)
	reposted := false

	// Track latest absolute position
	var lastAbsX, lastAbsY uint32
	gotAbs := false

	for dev.lastUsedIdx != usedIdx {
		ringIdx := dev.lastUsedIdx % dev.queueSize
		entryAddr := dev.usedVA + 4 + uintptr(ringIdx)*8
		descIdx := asm.MmioRead32(entryAddr)

		if descIdx < uint32(dev.queueSize) {
			evtAddr := dev.evtBufVA + uintptr(descIdx)*8
			evtType := asm.MmioRead16(evtAddr)
			evtCode := asm.MmioRead16(evtAddr + 2)
			evtValueLo := uint32(asm.MmioRead16(evtAddr + 4))
			evtValueHi := uint32(asm.MmioRead16(evtAddr + 6))
			evtValue := evtValueLo | (evtValueHi << 16)

			// Track absolute position for cursor
			if evtType == hid.EvAbs {
				if evtCode == hid.AbsX {
					lastAbsX = evtValue
					gotAbs = true
				} else if evtCode == hid.AbsY {
					lastAbsY = evtValue
					gotAbs = true
				}
			}

			// Push to ring for userspace consumption (same as mouse events)
			ev := hid.HIDEvent{Type: evtType, Code: evtCode, Value: evtValue}
			if ringPush(dev.ring, ev) {
				dev.dbgPushOK++
			} else {
				dev.dbgPushFail++
			}

			// Route through focus-based input system
			tabletClass := classifyInputEvent(nil, evtType, evtCode)
			if tabletClass >= 0 {
				routeInputEvent(ev, tabletClass)
			}

			// Repost buffer to device
			descAddr := dev.descVA + uintptr(descIdx)*16
			bufPA := uint64(dev.evtBufPA) + uint64(descIdx)*8
			asm.MmioWrite32(descAddr, uint32(bufPA))
			asm.MmioWrite32(descAddr+4, uint32(bufPA>>32))
			asm.MmioWrite32(descAddr+8, 8)
			asm.MmioWrite16(descAddr+12, 2) // VIRTQ_DESC_F_WRITE
			asm.MmioWrite16(descAddr+14, 0xFFFF)
			availRingIdx := dev.nextAvailIdx % dev.queueSize
			asm.MmioWrite16(dev.availVA+4+uintptr(availRingIdx)*2, uint16(descIdx))
			dev.nextAvailIdx++
			reposted = true
		}

		dev.lastUsedIdx++
	}

	// Sync LastUsedIdx
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

	// Move hardware cursor to latest absolute position
	if gotAbs {
		// Map tablet coordinates (0-32767) to screen coordinates
		screenX := (lastAbsX * gpu.DisplayWidth) / (hid.AbsMax + 1)
		screenY := (lastAbsY * gpu.DisplayHeight) / (hid.AbsMax + 1)
		gpu.TopHalfMoveCursor(screenX, screenY)
	}

	// Wake any soft IRQ consumer
	if reposted {
		WakeSlotForIRQ(dev.irqNum)
		// Tablet generates mouse-class events (clicks and movement)
		wakeInputConsumers(hid.InputClassMouseClick)
		wakeInputConsumers(hid.InputClassMouseMove)
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

// printInputIRQCounters prints the IRQ invocation counts for keyboard, mouse,
// and tablet devices, plus the used ring indices to diagnose event delivery.
func printInputIRQCounters() {
	serial.RawUARTPuts(" IN=")
	serial.RawUARTDecimal(uint64(topHalfKbd.dbgIRQCount))
	serial.RawUARTPuts("/")
	serial.RawUARTDecimal(uint64(topHalfMouse.dbgIRQCount))
	serial.RawUARTPuts("/")
	serial.RawUARTDecimal(uint64(topHalfTablet.dbgIRQCount))
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
