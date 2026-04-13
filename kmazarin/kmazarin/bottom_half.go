
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/hid"
	"mazzy/shared/iouring"
	"sync/atomic"
	"unsafe"
)

// buddyFreeHook calls kmem.BuddyFreeTyped via an indirect function pointer
// to break the nosplit chain in NonTimerIRQTopHalf. Initialized by InitTopHalfGCWake.
var buddyFreeHook func(pa uintptr, order int, pageType kmem.PageType)

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

// Debug counters for block IRQ instrumentation
var dbgBlockIRQCount uint32       // total block IRQs received
var dbgBlockIRQSync uint32        // handled in sync mode (IOComplete)
var dbgBlockIRQAsync uint32       // handled in async mode (ring push + wake)
var dbgBlockAsyncEvents uint32    // total async completion events pushed to ring

// Additional instrumentation counters (set by nosplit top-half, read by SVC path)
var dbgBlockTotalDrained uint32   // total completions drained across all IRQs
var dbgBlockEmptyIRQ uint32       // IRQs where HasUsed() was false (drained=0)
var dbgBlockRingFull uint32       // completion ring push failures (ring full)
var dbgBlockEmptyRawUsedIdx uint32  // raw Used.Idx on first empty-drain IRQ
var dbgBlockEmptyLastUsedIdx uint32 // LastUsedIdx at first empty-drain
var dbgBlockEmptyUsedPtr uint64     // VQ.Used pointer at first empty-drain
var dbgBlockEmptySnapped uint32     // 1 once the empty-drain snapshot is taken
var dbgBlockLastNumFree uint32    // last snapshot of VQ.NumFree
var dbgBlockLastUsedIdx uint32    // last snapshot of VQ.LastUsedIdx
var dbgBlockLastAvailIdx uint32   // last snapshot of VQ.Available.Idx
var dbgBlockCQEWritten uint32     // completions written to io_uring CQ
var dbgBlockCQEMissed uint32      // completions routed to legacy path (ioRing was nil)

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
	userData        uint64  // Opaque tag from io_uring SQEntry.UserData, written to CQEntry
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
func SetBlockAsyncSlot(tag uint16, sidecarStatusVA uintptr, sidecarIdx uint8, dataKernelVA uintptr, dataLen uint32, clumpAddr uintptr, userData uint64) {
	if tag < 256 {
		blockAsyncSlots[tag] = blockAsyncSlot{
			sidecarStatusVA: sidecarStatusVA,
			sidecarIdx:      sidecarIdx,
			dataKernelVA:    dataKernelVA,
			dataLen:         dataLen,
			clumpAddr:       clumpAddr,
			userData:        userData,
		}
	}
}

// EnableBlockAsyncMode switches the top-half to async completion delivery.
// Once enabled, SyscallBlockRead's interrupt path must not be used (falls back to polling).
func EnableBlockAsyncMode() {
	atomic.StoreUint32(&blockAsyncMode, 1)
}

// GetBlockIRQCount returns the total block IRQ count (for SVC-side logging).
func GetBlockIRQCount() uint32 { return atomic.LoadUint32(&dbgBlockIRQCount) }

// GetBlockTotalDrained returns the total completions drained (for SVC-side logging).
func GetBlockTotalDrained() uint32 { return atomic.LoadUint32(&dbgBlockTotalDrained) }

// GetBlockEmptyIRQ returns the count of IRQs with no used entries.
func GetBlockEmptyIRQ() uint32 { return atomic.LoadUint32(&dbgBlockEmptyIRQ) }

// GetBlockRingFull returns the count of completion ring push failures.
func GetBlockRingFull() uint32 { return atomic.LoadUint32(&dbgBlockRingFull) }

// GetBlockLastNumFree returns the last snapshot of VQ.NumFree.
func GetBlockLastNumFree() uint32 { return atomic.LoadUint32(&dbgBlockLastNumFree) }

// GetBlockEmptyRawUsedIdx returns the raw Used.Idx snapshot from the first empty-drain IRQ.
func GetBlockEmptyRawUsedIdx() uint32 { return atomic.LoadUint32(&dbgBlockEmptyRawUsedIdx) }

// GetBlockEmptyLastUsedIdx returns LastUsedIdx at the first empty-drain IRQ.
func GetBlockEmptyLastUsedIdx() uint32 { return atomic.LoadUint32(&dbgBlockEmptyLastUsedIdx) }

// GetBlockEmptyUsedPtr returns the VQ.Used VA at the first empty-drain IRQ.
func GetBlockEmptyUsedPtr() uint64 { return atomic.LoadUint64(&dbgBlockEmptyUsedPtr) }

// GetBlockEmptySnapped returns 1 if the empty-drain snapshot was taken.
func GetBlockEmptySnapped() uint32 { return atomic.LoadUint32(&dbgBlockEmptySnapped) }

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

// completionRingPush writes an event to the shared completion ring page.
// Acquires a spinlock for multi-core safety. If the ring is full, the event
// is dropped and an overflow counter is incremented.
//
//go:nosplit
//go:noinline
func completionRingPush(kva uintptr, ev hid.HIDEvent) bool {
	ring := (*hid.CompletionRing)(unsafe.Pointer(kva))
	// CAS spinlock acquire
	for !atomic.CompareAndSwapUint32(&ring.Lock, 0, 1) {
		asm.Wfe()
	}
	tail := atomic.LoadUint32(&ring.Tail)
	head := atomic.LoadUint32(&ring.Head)
	if tail-head >= hid.CompletionRingSize {
		atomic.AddUint32(&ring.Flags, 1) // overflow counter
		atomic.StoreUint32(&ring.Lock, 0)
		return false
	}
	ring.Events[tail%ring.Capacity] = ev
	asm.Dsb() // ensure event data visible before tail update
	atomic.StoreUint32(&ring.Tail, tail+1)
	atomic.StoreUint32(&ring.Lock, 0)
	return true
}

// priorityWakePending is set by uring IPC wakeups when a thread is woken.
// The non-timer IRQ return path in exceptions_arm64.s checks this flag and,
// if set, runs CheckThreadPreemption to immediately switch to the woken thread
// instead of returning to the interrupted thread and waiting for the next timer tick.
var priorityWakePending uint32

// Priority wake diagnostics — written from assembly, read from Go status printer
var dbgPWakeChecked uint32 // priorityWakePending was set when IRQ returned
var dbgPWakeEL1h uint32    // blocked by EL1h (SPSR.M[0]=1)
var dbgPWakeSVC uint32     // blocked by svcDepth != 0
var dbgPWakeNoG0 uint32    // g0 not ready
var dbgPWakeNoCtx uint32   // CheckThreadPreemption returned 0
var dbgPWakeSwitched uint32 // successfully switched to priority thread

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
		atomic.AddUint32(&dbgBlockIRQCount, 1)
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
			drained := uint32(0)

			for eng.HasUsed() {
				info := eng.PopUsed()
				if info.Tag == virtio.InvalidIOTag {
					break
				}
				drained++
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
						// Called via indirect pointer to break the nosplit chain.
						// BuddyFreeTyped has large stack frames (buddyRemoveSpecific →
						// buddyRemoveFree → duffzero) that exceed the 792-byte limit
						// when called from the ExceptionVectorTable chain.
						buddyFreeHook(clump.StartPA, clump.BuddyOrder, kmem.PageUserDMA)
					}
				}

				atomic.AddUint32(&dbgBlockAsyncEvents, 1)

				// Write completion: io_uring CQ if active, else legacy path.
				_, ioRing := GetIOUringSlotForBlockIRQ()
				if ioRing != nil {
					atomic.AddUint32(&dbgBlockCQEWritten, 1)
					cqTail := ioRing.CQTail
					cqIdx := cqTail & iouring.CQMask
					ioRing.CQEntries[cqIdx] = iouring.CQEntry{
						UserData: meta.userData,
						Res:      int32(info.UsedLen),
					}
					asm.Dsb()
					atomic.StoreUint32(&ioRing.CQTail, cqTail+1)
				} else {
					atomic.AddUint32(&dbgBlockCQEMissed, 1)
					ev := hid.HIDEvent{
						Type:  tag,
						Code:  status,
						Value: info.UsedLen,
					}
					crKVA := blockCompletionRingKVA
					if crKVA != 0 {
						if !completionRingPush(crKVA, ev) {
							atomic.AddUint32(&dbgBlockRingFull, 1)
						}
					} else {
						ringPush(&topHalfBlockRing, ev)
					}
				}

				// Clear metadata slot
				*meta = blockAsyncSlot{}
			}

			// Track drain stats via atomics (nosplit-safe, no printing)
			atomic.AddUint32(&dbgBlockTotalDrained, drained)
			if drained == 0 {
				atomic.AddUint32(&dbgBlockEmptyIRQ, 1)
				// Snapshot raw Used.Idx on first empty-drain (one-shot)
				if atomic.CompareAndSwapUint32(&dbgBlockEmptySnapped, 0, 1) {
					usedVA := uintptr(unsafe.Pointer(eng.VQ.Used))
					asm.InvalidateDCacheRange(usedVA, 4)
					atomic.StoreUint32(&dbgBlockEmptyRawUsedIdx, uint32(asm.MmioRead16(usedVA+2)))
					atomic.StoreUint32(&dbgBlockEmptyLastUsedIdx, uint32(eng.VQ.LastUsedIdx))
					atomic.StoreUint64(&dbgBlockEmptyUsedPtr, uint64(usedVA))
				}
			}
			// Snapshot VQ state for SVC-side logging
			atomic.StoreUint32(&dbgBlockLastNumFree, uint32(eng.VQ.NumFree))
			atomic.StoreUint32(&dbgBlockLastUsedIdx, uint32(eng.VQ.LastUsedIdx))
			atomic.StoreUint32(&dbgBlockLastAvailIdx, uint32(eng.VQ.Available.Idx))

			atomic.AddUint32(&dbgBlockIRQAsync, 1)
			// Wake: try io_uring direct wake, fall back to legacy slot.
			WakeIOUringFromIRQ()
			WakeSlotForIRQ(hid.BlockVirtualIRQ)
		} else {
			// Sync mode: just signal IOComplete for WFI loop
			atomic.AddUint32(&dbgBlockIRQSync, 1)
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

	// Get io_uring input ring once for the batch.
	_, inputRing := GetIOUringSlotForInputIRQ()
	cqPushed := false

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

			// Write HID event as io_uring CQE: type<<48 | code<<32 | value.
			if inputRing != nil {
				cqTail := inputRing.CQTail
				cqHead := atomic.LoadUint32(&inputRing.CQHead)
				if cqTail-cqHead < iouring.CQCapacity {
					cqIdx := cqTail & iouring.CQMask
					inputRing.CQEntries[cqIdx] = iouring.CQEntry{
						UserData: uint64(evtType)<<48 | uint64(evtCode)<<32 | uint64(evtValue),
					}
					asm.Dsb()
					atomic.StoreUint32(&inputRing.CQTail, cqTail+1)
					cqPushed = true
					dev.dbgPushOK++
				} else {
					dev.dbgPushFail++ // CQ full — event dropped
				}
			}

			// ALWAYS repost buffer to device, even if CQ push failed.
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

	// Sync VirtQueue.LastUsedIdx so DrainEvents does not re-drain.
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

	// Wake rachel's io_uring reader thread if we pushed any CQEs.
	if cqPushed {
		WakeIOUringFromIRQ()
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

	// Get io_uring input ring once for the batch.
	_, inputRing := GetIOUringSlotForInputIRQ()
	cqPushed := false

	// Track latest absolute position for hardware cursor.
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

			// Track absolute position for hardware cursor.
			if evtType == hid.EvAbs {
				if evtCode == hid.AbsX {
					lastAbsX = evtValue
					gotAbs = true
				} else if evtCode == hid.AbsY {
					lastAbsY = evtValue
					gotAbs = true
				}
			}

			// Write HID event as io_uring CQE: type<<48 | code<<32 | value.
			if inputRing != nil {
				cqTail := inputRing.CQTail
				cqHead := atomic.LoadUint32(&inputRing.CQHead)
				if cqTail-cqHead < iouring.CQCapacity {
					cqIdx := cqTail & iouring.CQMask
					inputRing.CQEntries[cqIdx] = iouring.CQEntry{
						UserData: uint64(evtType)<<48 | uint64(evtCode)<<32 | uint64(evtValue),
					}
					asm.Dsb()
					atomic.StoreUint32(&inputRing.CQTail, cqTail+1)
					cqPushed = true
					dev.dbgPushOK++
				} else {
					dev.dbgPushFail++ // CQ full — event dropped
				}
			}

			// Repost buffer to device.
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

	// Sync LastUsedIdx.
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

	// Move hardware cursor to latest absolute position.
	if gotAbs {
		screenX := (lastAbsX * gpu.DisplayWidth) / (hid.AbsMax + 1)
		screenY := (lastAbsY * gpu.DisplayHeight) / (hid.AbsMax + 1)
		gpu.TopHalfMoveCursor(screenX, screenY)
	}

	// Wake rachel's io_uring reader thread if we pushed any CQEs.
	if cqPushed {
		WakeIOUringFromIRQ()
	}
}

// ============================================================================
// Event Channels
// ============================================================================

var (
	uartRxEventChan       = make(chan struct{}, 1) // Buffered to avoid blocking poller
	deadlineEventChan     = make(chan struct{}, 1)
	pageTrackingEventChan = make(chan struct{}, 1)
	epochStatusChan       = make(chan struct{}, 1) // Epoch status request from timer top-half
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



// SetupUartSoftIRQ records the UART IRQ number so NonTimerIRQTopHalf
// can recognize it and drain the PL011 FIFO directly.
func SetupUartSoftIRQ(irqNum uint32) {
	uartIRQNum = irqNum
}

// ============================================================================
// Epoch Status Bottom Half
// ============================================================================

// epochStatusBottomHalf runs printEpochStatus in safe Go context.
// Bridged from ProcessDeadlinesTopHalf via epochStatusChan every ~10s.
// Deduplicates by comparing epochStatusCounter against a local copy,
// so multiple channel wakeups between prints produce only one output.
func epochStatusBottomHalf() {
	var lastSeen uint64
	for range epochStatusChan {
		cur := atomic.LoadUint64(&epochStatusCounter)
		if cur == lastSeen {
			continue
		}
		lastSeen = cur
		printEpochStatus()
	}
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
	go epochStatusBottomHalf()
}
