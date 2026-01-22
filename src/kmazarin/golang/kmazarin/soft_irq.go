//go:build arm64

package main

import (
	"kmazarin/ds"
	"kmazarin/kirq"
	"sync/atomic"
	"unsafe"
)

// ============================================================================
// Soft IRQ Dispatcher - Bridges Hardware IRQs to Go Goroutines
// ============================================================================
//
// This implements a soft IRQ system that allows device drivers to be written
// as regular Go code blocking on channels.
//
// Architecture:
//   Top Half (IRQ context, nosplit):
//     - Device IRQ fires → softIRQFire() called
//     - If dispatcher blocked: wake with priority (PushHead on ready queue)
//     - If dispatcher busy: queue to overflow buffer
//
//   Bottom Half (Go context):
//     - SoftIRQDispatcher goroutine calls WaitForSoftIRQ syscall
//     - Blocks until IRQ arrives
//     - Dispatches to registered subscriber channels

// SoftIRQBundle contains information about a delivered soft IRQ.
type SoftIRQBundle struct {
	IRQNum    uint32  // Hardware IRQ number (0-1019)
	Timestamp uint64  // CNTVCT_EL0 when IRQ fired
	DataPtr   uintptr // Device-specific data pointer
	Flags     uint32  // Reserved for future use
}

// ============================================================================
// Overflow Buffer - Queues IRQs when dispatcher is busy
// ============================================================================

const MaxPendingSoftIRQs = 64

// Overflow buffer storage
var softIRQOverflowData [MaxPendingSoftIRQs]SoftIRQBundle
var softIRQOverflowInUse [MaxPendingSoftIRQs]bool

// Queue indices for overflow buffer
var softIRQOverflowQueueData [MaxPendingSoftIRQs]int
var softIRQOverflowQueueInUse [MaxPendingSoftIRQs]bool
var softIRQOverflowQueue ds.StaticQueue[int]

// ============================================================================
// Dispatcher State
// ============================================================================

// softIRQDispatcherTID is the TID of the registered dispatcher thread.
// -1 means no dispatcher is registered.
var softIRQDispatcherTID ThreadId = -1

// softIRQDispatcherBlocked is 1 when the dispatcher is blocked waiting for IRQs.
// Accessed atomically from both IRQ context and Go context.
var softIRQDispatcherBlocked uint32 = 0

// ============================================================================
// Initialization
// ============================================================================

// InitSoftIRQ initializes the soft IRQ subsystem.
// Called during kernel startup.
//
//go:nosplit
func InitSoftIRQ() {
	softIRQOverflowQueue = ds.StaticQueue[int]{
		Data:  softIRQOverflowQueueData[:],
		InUse: softIRQOverflowQueueInUse[:],
	}
	softIRQDispatcherTID = -1
	atomic.StoreUint32(&softIRQDispatcherBlocked, 0)
}

// ============================================================================
// Top Half: softIRQFire (called from IRQ context)
// ============================================================================

// softIRQFire signals a soft IRQ from top-half context.
// Called by device IRQ handlers after minimal hardware interaction.
//
// MULTICORE SAFETY:
// - Atomic load on softIRQDispatcherBlocked (lock-free fast path check)
// - schedulerLock acquired only when waking dispatcher
// - DAIF already disabled in IRQ context
//
//go:nosplit
func softIRQFire(irqNum uint32, dataPtr uintptr) {
	if irqNum >= 1020 {
		return
	}

	bundle := SoftIRQBundle{
		IRQNum:    irqNum,
		Timestamp: kirq.ReadCounterValue(),
		DataPtr:   dataPtr,
		Flags:     0,
	}

	// Fast path: check if dispatcher is blocked
	if atomic.LoadUint32(&softIRQDispatcherBlocked) == 1 {
		softIRQWakeDispatcher(bundle)
		return
	}

	// Slow path: queue for later
	softIRQEnqueue(bundle)
}

// ============================================================================
// Wake Dispatcher with Priority
// ============================================================================

// softIRQWakeDispatcher wakes the blocked dispatcher with priority scheduling.
//
// MULTICORE SAFETY:
// - schedulerLock serializes all scheduler state modifications
// - Double-check softIRQDispatcherBlocked after lock acquisition
// - PushHead gives dispatcher priority over other ready threads
//
//go:nosplit
func softIRQWakeDispatcher(bundle SoftIRQBundle) {
	schedulerLock.Lock()

	// Double-check (another core may have woken it)
	if atomic.LoadUint32(&softIRQDispatcherBlocked) != 1 {
		schedulerLock.Unlock()
		softIRQEnqueue(bundle)
		return
	}

	t := threadList.FindByIdAll(int32(softIRQDispatcherTID)) // FindByIdAll to include kernel threads
	if t == nil || t.State != ThreadBlockedSoftIRQ {
		schedulerLock.Unlock()
		softIRQEnqueue(bundle)
		return
	}

	// Write bundle to dispatcher's buffer (FutexAddr holds the bundle pointer)
	bundlePtr := (*SoftIRQBundle)(unsafe.Pointer(uintptr(t.FutexAddr)))
	*bundlePtr = bundle

	// Wake with PRIORITY
	t.State = ThreadReady
	t.FutexAddr = 0
	atomic.StoreUint32(&softIRQDispatcherBlocked, 0)
	readyQueue.PushHead(softIRQDispatcherTID) // Priority insertion!

	schedulerLock.Unlock()
}

// ============================================================================
// Overflow Queue Management
// ============================================================================

// softIRQEnqueue adds bundle to overflow queue when dispatcher is busy.
//
// MULTICORE SAFETY: schedulerLock protects overflow queue.
//
//go:nosplit
func softIRQEnqueue(bundle SoftIRQBundle) {
	schedulerLock.Lock()

	// Find a free slot in the data array
	for i := 0; i < MaxPendingSoftIRQs; i++ {
		if !softIRQOverflowInUse[i] {
			softIRQOverflowData[i] = bundle
			softIRQOverflowInUse[i] = true
			softIRQOverflowQueue.Push(i)
			schedulerLock.Unlock()
			return
		}
	}

	// Queue full - drop (breadcrumb for debugging)
	schedulerLock.Unlock()
	Breadcrumb('!')
}

// GetPendingSoftIRQ drains one item from overflow queue.
// Returns true if bundle was available.
//
//go:nosplit
func GetPendingSoftIRQ(bundlePtr uint64) bool {
	schedulerLock.Lock()

	if softIRQOverflowQueue.IsEmpty() {
		schedulerLock.Unlock()
		return false
	}

	idx := softIRQOverflowQueue.Pop()
	bundle := softIRQOverflowData[idx]
	softIRQOverflowInUse[idx] = false

	destPtr := (*SoftIRQBundle)(unsafe.Pointer(uintptr(bundlePtr)))
	*destPtr = bundle

	schedulerLock.Unlock()
	return true
}

// ============================================================================
// Thread Blocking for Soft IRQ Wait
// ============================================================================

// ThreadBlockSoftIRQ blocks current thread waiting for soft IRQ.
// Returns context pointer of next thread, or 0 if none.
//
// MULTICORE SAFETY:
// - DAIF disabled prevents IRQ during state change
// - schedulerLock prevents concurrent scheduler access
// - softIRQDispatcherBlocked set AFTER state committed
//
//go:nosplit
func ThreadBlockSoftIRQ(sf *SchedulerFunc, bundlePtr uint64) uintptr {
	if CurrentThreadIdx < 0 {
		return 0
	}

	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// USERSPACE CHECK - panic for now, implement later
	if t.PageTableL0PA != 0 {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		KernelPanic("SYS_WAIT_SOFTIRQ: userspace not yet supported")
	}

	// Find next thread BEFORE blocking
	next := threadFindReadyIdx()
	if next == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Commit state change
	t.State = ThreadBlockedSoftIRQ
	t.FutexAddr = bundlePtr // Reuse FutexAddr for bundle pointer
	atomic.StoreUint32(&softIRQDispatcherBlocked, 1)

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// ============================================================================
// Dispatcher Registration
// ============================================================================

// RegisterSoftIRQDispatcher registers current thread as the dispatcher.
// Only one thread can be the dispatcher.
//
//go:nosplit
func RegisterSoftIRQDispatcher() int64 {
	schedulerLock.Lock()

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		return -22 // -EINVAL
	}
	currentTID := t.TID

	if softIRQDispatcherTID == -1 {
		softIRQDispatcherTID = currentTID
		schedulerLock.Unlock()
		return 0
	}

	if softIRQDispatcherTID == currentTID {
		schedulerLock.Unlock()
		return 0 // Already registered
	}

	schedulerLock.Unlock()
	return -22 // -EINVAL: another dispatcher exists
}

// ============================================================================
// Wrapper for Syscall Layer
// ============================================================================

// threadBlockSoftIRQWrapper is the ABI0-compatible wrapper for ThreadBlockSoftIRQ.
// Called via linkname from ksyscall package.
//
//go:nosplit
//go:noinline
func threadBlockSoftIRQWrapper(bundlePtr uint64) uintptr {
	return ThreadBlockSoftIRQ(&NormalSchedulerFunc, bundlePtr)
}
