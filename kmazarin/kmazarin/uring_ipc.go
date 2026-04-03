//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/ipc"
	"sync/atomic"
	"unsafe"
)

// ============================================================================
// Uring IPC — Per-Shepherd Message Ring for Inter-Shepherd Communication
// ============================================================================
//
// Each shepherd gets a kernel-allocated 3-page ring (64 × 128-byte slots).
// Multiple producers write via SysUringSend; a single consumer reads via
// SysUringRecv. The kernel holds a per-ring spinlock for producer serialization.
//
// Ring pages are kernel-owned and mapped into the owning shepherd's address
// space for the consumer to read directly. Producers write via the kernel
// (SysUringSend copies the message from the sender's user VA into the ring).

// nextUringID is the monotonically incrementing uring ID counter.
// 0 = invalid, assigned IDs start at 1.
var nextUringID uint64 = 1

// UringIPCSlot holds per-shepherd kernel state for the IPC ring.
type UringIPCSlot struct {
	KVA          [ipc.UringIPCPagesNeeded]uintptr // kernel VAs for the 3 ring pages
	PA           [ipc.UringIPCPagesNeeded]uintptr // physical addresses for cleanup
	OwnerSID     int16                            // shepherd that consumes from this ring (-1 = unused)
	BlockedTID   int16                            // TID blocked in SysUringRecv (-1 = none)
	BlockedPtr   uintptr                          // *Thread as uintptr (nosplit-safe pointer)
	RingRefCount int32                            // number of connections targeting this ring
	Dead         bool                             // owner terminated; pages freed when RingRefCount reaches 0
}

var uringIPCSlots [proc.MaxShepherds]UringIPCSlot

// UringConnection tracks an active connection between two shepherds.
type UringConnection struct {
	InUse         bool
	CallerSID     int16   // who established the connection
	TargetSID     int16   // whose ring the caller can send to
	RefCount      int32
	TargetRingKVA uintptr // KVA of target's ring page 0 (for fast send path)
}

const maxUringConnections = 128

var uringConnections [maxUringConnections]UringConnection

// uringIDEntry maps a uring ID to a shepherd ID.
type uringIDEntry struct {
	InUse   bool
	UringID uint64
	SID     int16
}

var uringIDMap [proc.MaxShepherds]uringIDEntry

func init() {
	for i := range uringIPCSlots {
		uringIPCSlots[i].OwnerSID = -1
		uringIPCSlots[i].BlockedTID = -1
	}
}

// ============================================================================
// Ring Allocation (called from DoRunShepherdWork on thread 0's stack)
// ============================================================================

// AllocUringIPCRing allocates 3 pages for a shepherd's IPC ring and
// maps them into both kernel scratch space and the shepherd's address space.
// Called from DoRunShepherdWork (heap-safe, on thread 0).
func AllocUringIPCRing(shepherd *proc.Shepherd) bool {
	sid := int16(shepherd.PID)
	if sid < 0 || int(sid) >= proc.MaxShepherds {
		return false
	}

	slot := &uringIPCSlots[sid]

	// Allocate 3 pages
	for i := 0; i < ipc.UringIPCPagesNeeded; i++ {
		pa := kmem.AllocPage(kmem.PageIPCBuffer, sid)
		if pa == 0 {
			serial.RawUARTPuts("[UringIPC] page alloc failed for SID ")
			serial.RawUARTDecimal(uint64(sid))
			serial.RawUARTPuts("\r\n")
			// Free previously allocated pages
			for j := 0; j < i; j++ {
				kmem.ReleasePageByPA(slot.PA[j])
			}
			return false
		}
		slot.PA[i] = pa

		// Zero the page
		kva := kmem.MapPAToKernelScratch(pa)
		if kva == 0 {
			serial.RawUARTPuts("[UringIPC] kernel scratch map failed\r\n")
			for j := 0; j <= i; j++ {
				kmem.ReleasePageByPA(slot.PA[j])
			}
			return false
		}
		slot.KVA[i] = kva

		// Zero the page via kernel VA
		ptr := (*[4096]byte)(unsafe.Pointer(kva))
		for b := range ptr {
			ptr[b] = 0
		}
	}

	// Initialize ring header on page 0
	hdr := (*ipc.UringIPCRingHeader)(unsafe.Pointer(slot.KVA[0]))
	hdr.Head = 0
	hdr.Tail = 0
	hdr.Capacity = ipc.UringIPCCapacity
	hdr.RingMask = ipc.UringIPCMask
	hdr.OwnerSID = sid
	hdr.OwnerID = shepherd.UringID
	hdr.Lock = 0

	slot.OwnerSID = sid
	slot.BlockedTID = -1
	slot.BlockedPtr = 0

	// Store PA of first page in shepherd struct for reference
	shepherd.UringRingPA = slot.PA[0]

	serial.RawUARTPuts("[UringIPC] ring allocated for SID ")
	serial.RawUARTDecimal(uint64(sid))
	serial.RawUARTPuts(" uringID=")
	serial.RawUARTDecimal(shepherd.UringID)
	serial.RawUARTPuts("\r\n")

	return true
}

// RegisterUringID records a (uringID → SID) mapping.
func RegisterUringID(id uint64, sid int16) {
	for i := range uringIDMap {
		if !uringIDMap[i].InUse {
			uringIDMap[i] = uringIDEntry{InUse: true, UringID: id, SID: sid}
			return
		}
	}
	serial.RawUARTPuts("[UringIPC] ID map full!\r\n")
}

// lookupUringID resolves a uring ID to a SID. Returns (sid, true) or (-1, false).
//
//go:nosplit
func lookupUringID(id uint64) (int16, bool) {
	for i := range uringIDMap {
		if uringIDMap[i].InUse && uringIDMap[i].UringID == id {
			return uringIDMap[i].SID, true
		}
	}
	return -1, false
}

// AllocateUringID atomically allocates the next uring ID.
//
//go:nosplit
func AllocateUringID() uint64 {
	return atomic.AddUint64(&nextUringID, 1) - 1
}

// ============================================================================
// Connection Management (DoUringConnectWork runs on thread 0)
// ============================================================================

// UringConnectWorkRequest is the request struct for the KernelSVCWorker.
type UringConnectWorkRequest struct {
	TargetUringID uint64
	CallerSID     int16
}

// DoUringConnectWork processes a SysUringConnect request on thread 0's stack.
// Looks up the target uring ID, validates the target ring exists, and creates
// a connection entry with refcount 1.
// Returns handle (connection index) on success, or negative errno.
func DoUringConnectWork(req *UringConnectWorkRequest) int64 {
	targetSID, ok := lookupUringID(req.TargetUringID)
	if !ok {
		serial.RawUARTPuts("[UringIPC] Connect: unknown uringID ")
		serial.RawUARTDecimal(req.TargetUringID)
		serial.RawUARTPuts("\r\n")
		return -3 // ESRCH
	}

	if targetSID == req.CallerSID {
		return -22 // EINVAL — can't connect to self
	}

	// Verify target ring exists
	if targetSID < 0 || int(targetSID) >= proc.MaxShepherds {
		return -3 // ESRCH
	}
	slot := &uringIPCSlots[targetSID]
	if slot.OwnerSID != targetSID || slot.KVA[0] == 0 {
		return -3 // ESRCH — ring not allocated
	}

	// Check for existing connection (bump refcount)
	for i := range uringConnections {
		if uringConnections[i].InUse &&
			uringConnections[i].CallerSID == req.CallerSID &&
			uringConnections[i].TargetSID == targetSID {
			uringConnections[i].RefCount++
			serial.RawUARTPuts("[UringIPC] Connect: reuse handle=")
			serial.RawUARTDecimal(uint64(i))
			serial.RawUARTPuts(" ref=")
			serial.RawUARTDecimal(uint64(uringConnections[i].RefCount))
			serial.RawUARTPuts("\r\n")
			return int64(i)
		}
	}

	// Allocate new connection
	for i := range uringConnections {
		if !uringConnections[i].InUse {
			uringConnections[i] = UringConnection{
				InUse:         true,
				CallerSID:     req.CallerSID,
				TargetSID:     targetSID,
				RefCount:      1,
				TargetRingKVA: slot.KVA[0],
			}
			slot.RingRefCount++
			serial.RawUARTPuts("[UringIPC] Connect: new handle=")
			serial.RawUARTDecimal(uint64(i))
			serial.RawUARTPuts(" caller=")
			serial.RawUARTDecimal(uint64(req.CallerSID))
			serial.RawUARTPuts(" target=")
			serial.RawUARTDecimal(uint64(targetSID))
			serial.RawUARTPuts(" ringRef=")
			serial.RawUARTDecimal(uint64(slot.RingRefCount))
			serial.RawUARTPuts("\r\n")
			return int64(i)
		}
	}

	return -12 // ENOMEM — connection table full
}

// ============================================================================
// Ring Write (Producer Path)
// ============================================================================

// ringSlotKVA returns the kernel VA of slot[index] in the ring.
// The ring layout is: 128-byte header at page 0 offset 0, then slots starting
// at offset 128. Slots 0-31 are on page 0 (offsets 0x80-0x1000),
// slots 32-63 span pages 1-2.
//
//go:nosplit
func ringSlotKVA(slot *UringIPCSlot, index uint32) uintptr {
	byteOffset := uintptr(ipc.UringIPCHeaderSize) + uintptr(index)*uintptr(ipc.UringIPCSlotSize)
	pageIdx := byteOffset / kmem.PageSize
	pageOff := byteOffset % kmem.PageSize
	return slot.KVA[pageIdx] + pageOff
}

// ringHeader returns the ring header pointer from page 0.
//
//go:nosplit
func ringHeader(slot *UringIPCSlot) *ipc.UringIPCRingHeader {
	return (*ipc.UringIPCRingHeader)(unsafe.Pointer(slot.KVA[0]))
}

// acquireProducerLock spins on the ring's producer lock.
// MUST be called with IRQs disabled. Short spin — only held for one 128-byte copy.
//
//go:nosplit
func acquireProducerLock(hdr *ipc.UringIPCRingHeader) {
	for !atomic.CompareAndSwapInt32(&hdr.Lock, 0, 1) {
		// Spin — lock is held for at most ~128 bytes of memcpy + tail advance.
	}
}

// releaseProducerLock releases the producer lock.
//
//go:nosplit
func releaseProducerLock(hdr *ipc.UringIPCRingHeader) {
	atomic.StoreInt32(&hdr.Lock, 0)
}

// UringSendKernel writes a 128-byte message to the target's ring and wakes
// the blocked receiver if any. msgKVA points to a kernel-accessible copy of
// the message (already copied from userspace by the syscall handler).
//
// Returns (0, ctxPtr) on success, (negative errno, 0) on failure.
// ctxPtr is the woken thread's context pointer for immediate switch, or 0.
//
//go:noinline
func UringSendKernel(senderSID, targetSID int16, msgKVA uintptr) (int64, uintptr) {
	if targetSID < 0 || int(targetSID) >= proc.MaxShepherds {
		return -22, 0 // EINVAL
	}

	slot := &uringIPCSlots[targetSID]
	if slot.OwnerSID != targetSID || slot.KVA[0] == 0 || slot.Dead {
		return -3, 0 // ESRCH — no ring for target (or owner dead)
	}

	hdr := ringHeader(slot)

	savedDAIF := SaveAndDisableIRQs()

	acquireProducerLock(hdr)

	// Check ring full
	head := atomic.LoadUint32(&hdr.Head)
	tail := atomic.LoadUint32(&hdr.Tail)
	if tail-head >= ipc.UringIPCCapacity {
		releaseProducerLock(hdr)
		RestoreIRQs(savedDAIF)
		return -11, 0 // EAGAIN — ring full
	}

	// Write message to slot[tail & mask]
	slotIdx := tail & ipc.UringIPCMask
	dstKVA := ringSlotKVA(slot, slotIdx)

	// Copy 128 bytes from kernel buffer to ring slot
	src := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(msgKVA))
	dst := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(dstKVA))
	*dst = *src

	// Advance tail
	atomic.StoreUint32(&hdr.Tail, tail+1)

	releaseProducerLock(hdr)

	// Wake blocked receiver if any
	var wokenCtx uintptr
	schedulerLock.Lock()

	if slot.BlockedTID >= 0 {
		t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
		if t != nil && t.State == ThreadBlockedUringRecv {
			t.State = ThreadReady
			t.PriorityWoken = true
			slot.BlockedTID = -1
			slot.BlockedPtr = 0
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
			enqueueReadyPrioritySchedLockHeld(t)
			wokenCtx = uintptr(unsafe.Pointer(&t.Context))
			asm.Dsb()
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	return 0, wokenCtx
}

// KernelWriteToRing writes a message directly to a shepherd's ring without
// a sending shepherd context. Used for kernel→shepherd messages (death
// notifications, FS dispatch in Phase 4).
//
//go:noinline
func KernelWriteToRing(targetSID int16, msg *ipc.UringIPCMsg) (int64, uintptr) {
	return UringSendKernel(-1, targetSID, uintptr(unsafe.Pointer(msg)))
}

// KernelWriteToRingFromIRQ is a nosplit-safe version of KernelWriteToRing
// for use from IRQ top-half handlers. Instead of returning the woken thread's
// context pointer, it sets the priorityWakePending flag so the IRQ return
// path triggers an immediate context switch.
//
// MUST be called with IRQs already disabled (top-half context).
// The caller MUST already hold no locks (schedulerLock is acquired internally).
//
//go:nosplit
//go:noinline
func KernelWriteToRingFromIRQ(targetSID int16, msg *ipc.UringIPCMsg) {
	if targetSID < 0 || int(targetSID) >= proc.MaxShepherds {
		return
	}

	slot := &uringIPCSlots[targetSID]
	if slot.OwnerSID != targetSID || slot.KVA[0] == 0 {
		return
	}

	hdr := ringHeader(slot)

	acquireProducerLock(hdr)

	// Check ring full
	head := atomic.LoadUint32(&hdr.Head)
	tail := atomic.LoadUint32(&hdr.Tail)
	if tail-head >= ipc.UringIPCCapacity {
		releaseProducerLock(hdr)
		return // drop message if ring full
	}

	// Write message to slot
	slotIdx := tail & ipc.UringIPCMask
	dstKVA := ringSlotKVA(slot, slotIdx)
	src := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(msg))
	dst := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(dstKVA))
	*dst = *src

	// Advance tail
	atomic.StoreUint32(&hdr.Tail, tail+1)
	releaseProducerLock(hdr)

	// Wake blocked receiver if any
	schedulerLock.Lock()

	if slot.BlockedTID >= 0 {
		t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
		if t != nil && t.State == ThreadBlockedUringRecv {
			t.State = ThreadReady
			t.PriorityWoken = true
			slot.BlockedTID = -1
			slot.BlockedPtr = 0
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
			enqueueReadyPrioritySchedLockHeld(t)
			atomic.StoreUint32(&priorityWakePending, 1)
			asm.Dsb()
		}
	}

	schedulerLock.Unlock()
}

// ============================================================================
// Ring Read (Consumer Path — Blocking)
// ============================================================================

// drainUringIPCRing pops one message from the shepherd's ring.
// Returns (msg KVA, true) or (0, false) if empty.
// The returned KVA points into the ring buffer — caller must copy before
// advancing head.
//
//go:nosplit
func drainUringIPCRing(sid int16) (uintptr, bool) {
	if sid < 0 || int(sid) >= proc.MaxShepherds {
		return 0, false
	}
	slot := &uringIPCSlots[sid]
	if slot.KVA[0] == 0 {
		return 0, false
	}

	hdr := ringHeader(slot)
	head := atomic.LoadUint32(&hdr.Head)
	tail := atomic.LoadUint32(&hdr.Tail)
	if head == tail {
		return 0, false // empty
	}

	slotIdx := head & ipc.UringIPCMask
	msgKVA := ringSlotKVA(slot, slotIdx)

	// Advance head AFTER caller copies (caller calls advanceUringHead)
	return msgKVA, true
}

// advanceUringHead advances the consumer head pointer after the message
// has been copied to userspace.
//
//go:nosplit
func advanceUringHead(sid int16) {
	if sid < 0 || int(sid) >= proc.MaxShepherds {
		return
	}
	hdr := ringHeader(&uringIPCSlots[sid])
	head := atomic.LoadUint32(&hdr.Head)
	atomic.StoreUint32(&hdr.Head, head+1)
}

// BlockForUringRecv blocks the current thread waiting for a message on its
// shepherd's IPC uring ring. bufPtr is saved for SVC rewind.
// Returns the context pointer of the next thread, or 0.
//
//go:nosplit
//go:noinline
func BlockForUringRecv(shepherdIdx int, bufPtr uint64) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Clear previous blocked thread (M migration)
	if shepherdIdx >= 0 && shepherdIdx < proc.MaxShepherds {
		prev := (*Thread)(unsafe.Pointer(uringIPCSlots[shepherdIdx].BlockedPtr))
		if prev != nil && prev.State == ThreadBlockedUringRecv {
			prev.State = ThreadReady
			enqueueReadySchedLockHeld(prev)
		}
	}

	t.State = ThreadBlockedUringRecv
	t.SoftIRQSlotArg = bufPtr    // arg0 for rewind
	t.SoftIRQSyscallNum = 0x1015 // SysUringRecv
	if shepherdIdx >= 0 && shepherdIdx < proc.MaxShepherds {
		uringIPCSlots[shepherdIdx].BlockedTID = int16(t.TID)
		uringIPCSlots[shepherdIdx].BlockedPtr = uintptr(unsafe.Pointer(t))
	}

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// ============================================================================
// Connection Release
// ============================================================================

// ReleaseUringConnection decrements the refcount on a connection.
// If refcount reaches 0, the connection slot is freed.
// Returns 0 on success, negative errno on failure.
//
//go:nosplit
func ReleaseUringConnection(handle int, callerSID int16) int64 {
	if handle < 0 || handle >= maxUringConnections {
		return -22 // EINVAL
	}
	conn := &uringConnections[handle]
	if !conn.InUse || conn.CallerSID != callerSID {
		return -22 // EINVAL — not owned by caller
	}

	conn.RefCount--
	if conn.RefCount <= 0 {
		targetSID := conn.TargetSID
		conn.InUse = false
		conn.CallerSID = -1
		conn.TargetSID = -1
		conn.RefCount = 0
		conn.TargetRingKVA = 0

		// Decrement target ring's refcount. If the ring owner is dead
		// and this was the last reference, free the ring pages.
		if targetSID >= 0 && int(targetSID) < proc.MaxShepherds {
			slot := &uringIPCSlots[targetSID]
			slot.RingRefCount--
			if slot.Dead && slot.RingRefCount <= 0 {
				freeUringRingPages(slot)
			}
		}
	}

	return 0
}

// ============================================================================
// Cleanup (called from TerminateShepherd)
// ============================================================================

// freeUringRingPages releases the physical pages backing a shepherd's uring ring
// and zeroes the slot. Called when the ring owner is dead and all references
// (connections targeting this ring) have been released.
func freeUringRingPages(slot *UringIPCSlot) {
	for i := 0; i < ipc.UringIPCPagesNeeded; i++ {
		if slot.PA[i] != 0 {
			kmem.ReleasePageByPA(slot.PA[i])
			slot.PA[i] = 0
			slot.KVA[i] = 0
		}
	}
	slot.OwnerSID = -1
	slot.Dead = false
	slot.RingRefCount = 0
}

// CleanupUringIPCForShepherd handles uring teardown when a shepherd dies.
//
// 1. Wake any thread blocked in SysUringRecv on the dying shepherd's ring.
// 2. Send ProtoDeath to all peers that hold connections TO the dead shepherd.
// 3. Free all connections FROM the dead shepherd (decrement target ring refcounts).
// 4. Mark ring Dead. Free pages only if RingRefCount is already 0.
// 5. Clear the uring ID map entry.
func CleanupUringIPCForShepherd(sid int16) {
	if sid < 0 || int(sid) >= proc.MaxShepherds {
		return
	}

	slot := &uringIPCSlots[sid]

	// 1. Wake any thread blocked on the dying shepherd's uring.
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if slot.BlockedTID >= 0 {
		t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
		if t != nil && t.State == ThreadBlockedUringRecv {
			t.State = ThreadReady
			enqueueReadySchedLockHeld(t)
		}
		slot.BlockedTID = -1
		slot.BlockedPtr = 0
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	// 2. Send ProtoDeath to peers connected TO the dead shepherd.
	// These are connections where TargetSID == sid (someone else connected to us).
	deathMsg := ipc.EncodeDeathNotification(sid)
	for i := range uringConnections {
		conn := &uringConnections[i]
		if conn.InUse && conn.TargetSID == sid {
			// Deliver death notification to the caller's ring.
			KernelWriteToRing(conn.CallerSID, &deathMsg)
		}
	}

	// 3. Free connections FROM the dead shepherd (we can no longer Release them).
	// Also free connections TO the dead shepherd (peers will Release later,
	// but the connection is useless now — sends will fail with ESRCH).
	for i := range uringConnections {
		conn := &uringConnections[i]
		if !conn.InUse {
			continue
		}
		if conn.CallerSID == sid {
			// Connection FROM the dead shepherd to some target.
			// Decrement target ring's refcount.
			targetSID := conn.TargetSID
			if targetSID >= 0 && int(targetSID) < proc.MaxShepherds {
				targetSlot := &uringIPCSlots[targetSID]
				targetSlot.RingRefCount--
				if targetSlot.Dead && targetSlot.RingRefCount <= 0 {
					freeUringRingPages(targetSlot)
				}
			}
			conn.InUse = false
			conn.RefCount = 0
			conn.CallerSID = -1
			conn.TargetSID = -1
			conn.TargetRingKVA = 0
		} else if conn.TargetSID == sid {
			// Connection TO the dead shepherd. Mark it dead so sends fail.
			// Don't free the connection — the caller owns it and will Release.
			// But decrement our ring's refcount now.
			slot.RingRefCount--
		}
	}

	// 4. Mark ring Dead. Free pages only if no outstanding references.
	if slot.RingRefCount <= 0 {
		freeUringRingPages(slot)
	} else {
		slot.Dead = true
	}

	// 5. Clear the ID map entry.
	for i := range uringIDMap {
		if uringIDMap[i].InUse && uringIDMap[i].SID == sid {
			uringIDMap[i].InUse = false
		}
	}

	serial.RawUARTPuts("[UringIPC] cleaned for shepherd ")
	serial.RawUARTDecimal(uint64(sid))
	if slot.Dead {
		serial.RawUARTPuts(" (deferred, ringRef=")
		serial.RawUARTDecimal(uint64(slot.RingRefCount))
		serial.RawUARTPuts(")")
	}
	serial.RawUARTPuts("\r\n")
}
