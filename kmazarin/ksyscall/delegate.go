package ksyscall

// delegate.go — Syscall delegation: priests register to handle specific syscalls.
//
// A priest registers for one or more SysIDs (Write, Read, Openat, Close, etc.).
// The kernel intercepts matching syscalls from other priests and forwards them.
//
// Data page lifecycle (owned by the kernel throughout):
//   - Write: kernel allocs page, copies caller buffer in, maps into handler.
//             Handler reads data, replies. Kernel reclaims page.
//   - Read:  kernel allocs empty page, maps into handler.
//             Handler fills page with result, replies with byte count.
//             Kernel copies bytes to caller's original buffer, reclaims page.
//   - Openat/Close: no data page, just args and return value.

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

// MaxDelegateQueueDepth is the max queued delegation requests per SysID.
const MaxDelegateQueueDepth = 8

// MaxDelegateThreads is the max concurrent delegated calls in flight.
const MaxDelegateThreads = 512

// delegateHandler maps a SysID to the priest that handles it.
// pid is int32 (not int16) because RISC-V lr.w/sc.w atomics require
// 4-byte alignment. With int16, odd-indexed array elements would be
// at 2-byte boundaries, causing misaligned load traps (scause=4).
type delegateHandler struct {
	pid int32 // handler priest PID (-1 = unregistered)
}

// delegateRecvState tracks a handler priest's blocked recv thread.
// Per-priest (not per-SysID) so one recv loop handles all registered syscalls.
type delegateRecvState struct {
	recvTID   int16   // TID blocked in DelegatedRecv (-1 = none)
	resultPtr uintptr // VA of result struct in handler's address space
}

// DelegateQueueEntry is a queued delegated syscall waiting for the handler.
type DelegateQueueEntry struct {
	CallerPID  proc.PriestId
	CallerTID  int16
	SysID      sysid.ID
	Args       [6]uint64
	DataPagePA uintptr // PA of the data page (0 if no data)
	DataVA     uint64  // VA of data page in the handler's address space
	DataLen    uint32  // Bytes of data in the page (Write: caller data; Read: 0 initially)
}

// DelegateCallInfo records per-caller-thread state while a delegated syscall
// is in flight. Used by the reply path to copy data back (Read) and reclaim pages.
type DelegateCallInfo struct {
	DataPagePA   uintptr  // PA of data page (0 = no page)
	DataPageVA   uint64   // VA of data page in handler's address space
	HandlerPID   int16    // Handler priest PID (to unmap from)
	CallerBufVA  uintptr  // Caller's original buffer VA (Read: copy-back destination)
	CallerBufLen uint32   // Max bytes caller requested (Read: cap for copy-back)
	CallerL0PA   uintptr  // Caller's L0 page table PA (for copy-back)
	SysID        sysid.ID // Which syscall (determines copy direction)
	InUse        bool
}

// syscallDelegates maps SysID → handler priest PID.
var syscallDelegates [sysid.NumIDs]delegateHandler

// delegateRecvStates tracks recv state per handler priest (indexed by PID).
var delegateRecvStates [proc.MaxPriests]delegateRecvState

// Per-SysID delegation queues.
var delegateQueues [sysid.NumIDs]struct {
	entries [MaxDelegateQueueDepth]DelegateQueueEntry
	head    uint32
	tail    uint32
}

// delegateCallInfos tracks in-flight delegated calls (indexed by caller TID).
var delegateCallInfos [MaxDelegateThreads]DelegateCallInfo

func init() {
	for i := range syscallDelegates {
		syscallDelegates[i].pid = -1
	}
	for i := range delegateRecvStates {
		delegateRecvStates[i].recvTID = -1
	}
}

// IsDelegated returns true if the given SysID has a handler priest registered
// and the caller is not the handler itself.
//
//go:nosplit
func IsDelegated(id sysid.ID, callerPID int16) bool {
	if id == sysid.Invalid || id >= sysid.NumIDs {
		return false
	}
	hpid := atomic.LoadInt32(&syscallDelegates[id].pid)
	return hpid >= 0 && int16(hpid) != callerPID
}

// DelegateSyscall forwards a syscall to the registered handler priest.
// Allocates a data page for syscalls that transfer data (Write, Read).
// Blocks the caller until the handler replies.
//
//go:noinline
func DelegateSyscall(id sysid.ID, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	handlerPID := int16(atomic.LoadInt32(&syscallDelegates[id].pid))
	if handlerPID < 0 {
		return -38 // ENOSYS
	}

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}
	_, callerTID := getCurrentThreadPIDAndTID()

	handlerPriest := proc.FindPriestByPID(proc.PriestId(handlerPID))
	if handlerPriest == nil {
		return -3 // ESRCH
	}

	var dataPagePA uintptr
	var handlerDataVA uint64
	var dataLen uint32

	switch id {
	case sysid.Write:
		// Write: copy caller's buffer into data page
		if arg1 != 0 && arg2 > 0 {
			count := arg2
			if count > 4096 {
				count = 4096
			}
			pa, va, n := allocAndCopyCallerData(handlerPID, handlerPriest, uintptr(arg1), count)
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Read:
		// Read: allocate empty page for handler to fill
		if arg2 > 0 {
			count := arg2
			if count > 4096 {
				count = 4096
			}
			pa, va := allocEmptyDataPage(handlerPID, handlerPriest)
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(count) // max bytes handler can write
		}

	case sysid.Openat:
		// Openat: copy pathname string into data page.
		// arg0 = dirfd, arg1 = pathname pointer, arg2 = flags, arg3 = mode
		if arg1 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerPID, handlerPriest, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	default:
		// Close, etc: no data page
	}

	// Enqueue the request
	entry := DelegateQueueEntry{
		CallerPID:  callerPriest.PID,
		CallerTID:  callerTID,
		SysID:      id,
		DataPagePA: dataPagePA,
		DataVA:     handlerDataVA,
		DataLen:    dataLen,
	}
	entry.Args = [6]uint64{arg0, arg1, arg2, arg3, arg4, arg5}

	q := &delegateQueues[id]
	next := (q.tail + 1) % MaxDelegateQueueDepth
	if next == q.head {
		reclaimDataPage(dataPagePA, handlerDataVA, handlerPID, handlerPriest)
		serial.RawUARTPuts("[DLG] queue full\r\n")
		return -11 // EAGAIN
	}
	q.entries[q.tail] = entry
	q.tail = next

	// Stash call info for the reply path (indexed by caller TID)
	if int(callerTID) < MaxDelegateThreads {
		info := &delegateCallInfos[callerTID]
		info.DataPagePA = dataPagePA
		info.DataPageVA = handlerDataVA
		info.HandlerPID = handlerPID
		info.CallerBufVA = uintptr(arg1)
		info.CallerBufLen = uint32(arg2)
		info.CallerL0PA = callerPriest.PageTableL0PA
		info.SysID = id
		info.InUse = true
	}

	serial.RawUARTPuts("D")

	// If handler has a thread blocked in recv, wake it
	if handlerPID >= 0 && int(handlerPID) < proc.MaxPriests {
		rs := &delegateRecvStates[handlerPID]
		if rs.recvTID >= 0 {
			recvTID := rs.recvTID
			resultPtr := rs.resultPtr
			rs.recvTID = -1
			rs.resultPtr = 0

			// Dequeue and deliver to the handler
			e := delegateQueuePop(id)
			if e != nil {
				writeDelegateRecvResult(resultPtr, handlerPriest.PageTableL0PA, e)
				wakeDelegateThread(int32(recvTID), 0)
			}
		}
	}

	// Block the caller
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	// Return value is set by the reply handler (WakeDelegateCallerThread)
	return 0
}

// allocAndCopyCallerData allocates a page, copies caller data into it, and
// maps it into the handler's address space. Returns (PA, handlerVA, bytesCopied).
func allocAndCopyCallerData(handlerPID int16, handlerPriest *proc.Priest, callerBufVA uintptr, count uint64) (uintptr, uint64, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerPID)
	if pa == 0 {
		return 0, 0, 0
	}

	scratchVA := kmem.MapPAToKernelScratch(pa)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}

	// Zero the page first
	zeroPage(scratchVA)

	// Copy caller data
	dst := unsafe.Slice((*byte)(unsafe.Pointer(scratchVA)), count)
	if !kmem.CopyFromUser(dst, callerBufVA, int(count)) {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}

	va := bumpAllocForPriest(handlerPriest, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	kmem.MapPageInProcess(handlerPID, uintptr(va), pa, 0) // RW
	handlerPriest.Spans.Add(va, 4096)

	return pa, va, count
}

// allocEmptyDataPage allocates a zeroed page and maps it into the handler's space.
// The handler will fill it with data (for Read).
func allocEmptyDataPage(handlerPID int16, handlerPriest *proc.Priest) (uintptr, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerPID)
	if pa == 0 {
		return 0, 0
	}

	scratchVA := kmem.MapPAToKernelScratch(pa)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0
	}
	zeroPage(scratchVA)

	va := bumpAllocForPriest(handlerPriest, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0
	}
	kmem.MapPageInProcess(handlerPID, uintptr(va), pa, 0) // RW
	handlerPriest.Spans.Add(va, 4096)

	return pa, va
}

// allocAndCopyCallerString allocates a data page and copies a null-terminated
// string from the caller's address space into it. Copies up to the end of the
// source page to avoid crossing into potentially unmapped memory.
// The data page is pre-zeroed, guaranteeing null termination.
// Returns (PA, handlerVA, stringLength).
func allocAndCopyCallerString(handlerPID int16, handlerPriest *proc.Priest, callerStrVA uintptr) (uintptr, uint64, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerPID)
	if pa == 0 {
		return 0, 0, 0
	}

	scratchVA := kmem.MapPAToKernelScratch(pa)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	zeroPage(scratchVA)

	// Copy up to the end of the caller's current page.
	// This avoids faulting on an adjacent unmapped page.
	maxCopy := uintptr(4096) - (callerStrVA & 0xFFF)
	if maxCopy > 4096 {
		maxCopy = 4096
	}

	dst := unsafe.Slice((*byte)(unsafe.Pointer(scratchVA)), maxCopy)
	if !kmem.CopyFromUser(dst, callerStrVA, int(maxCopy)) {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}

	// Find the null terminator to determine actual string length
	var strLen uint64
	for i := uintptr(0); i < maxCopy; i++ {
		if dst[i] == 0 {
			strLen = uint64(i)
			break
		}
	}
	if strLen == 0 && maxCopy > 0 && dst[0] != 0 {
		strLen = uint64(maxCopy)
	}

	va := bumpAllocForPriest(handlerPriest, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	kmem.MapPageInProcess(handlerPID, uintptr(va), pa, 0) // RW
	handlerPriest.Spans.Add(va, 4096)

	return pa, va, strLen
}

// zeroPage zeroes a 4096-byte page at the given kernel VA.
func zeroPage(va uintptr) {
	p := (*[4096]byte)(unsafe.Pointer(va))
	for i := range p {
		p[i] = 0
	}
}

// reclaimDataPage unmaps and frees a data page on error paths.
func reclaimDataPage(pa uintptr, handlerVA uint64, handlerPID int16, handlerPriest *proc.Priest) {
	if pa == 0 {
		return
	}
	if handlerVA != 0 && handlerPriest != nil {
		kmem.UnmapUserPageWithL0(uintptr(handlerVA), handlerPriest.PageTableL0PA)
		handlerPriest.Spans.Remove(handlerVA, 4096)
	}
	kmem.ReleasePageByPA(pa)
}

// delegateQueuePop dequeues the next entry for a given SysID.
func delegateQueuePop(id sysid.ID) *DelegateQueueEntry {
	q := &delegateQueues[id]
	if q.head == q.tail {
		return nil
	}
	e := &q.entries[q.head]
	q.head = (q.head + 1) % MaxDelegateQueueDepth
	return e
}

// delegateQueuePopAnyForPriest scans all SysID queues for entries targeting
// the given handler priest. Returns the first found, or nil.
func delegateQueuePopAnyForPriest(handlerPID int16) *DelegateQueueEntry {
	for i := sysid.ID(0); i < sysid.NumIDs; i++ {
		hpid := int16(atomic.LoadInt32(&syscallDelegates[i].pid))
		if hpid != handlerPID {
			continue
		}
		if e := delegateQueuePop(i); e != nil {
			return e
		}
	}
	return nil
}

// SyscallRegisterSyscallHandler registers the calling priest as the handler
// for a specific SysID. Can be called multiple times for different SysIDs.
//
// arg0 = SysID to handle
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterSyscallHandler(arg0, _, _, _, _, _ uint64) int64 {
	id := sysid.ID(arg0)
	if id == sysid.Invalid || id >= sysid.NumIDs {
		return -22 // EINVAL
	}

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}

	h := &syscallDelegates[id]
	existing := atomic.LoadInt32(&h.pid)
	if existing >= 0 {
		return -16 // EBUSY — already registered
	}

	atomic.StoreInt32(&h.pid, int32(callerPriest.PID))

	serial.RawUARTPuts("[DLG] P")
	serial.RawUARTDecimal(uint64(callerPriest.PID))
	serial.RawUARTPuts(" handles SysID ")
	serial.RawUARTDecimal(uint64(id))
	serial.RawUARTPuts("\r\n")

	return 0
}

// SyscallDelegatedRecv blocks until a delegated syscall request arrives
// for any SysID this priest handles.
//
// arg0 = pointer to DelegateRecvResult struct in userspace:
//        {SysID uint16, CallerPID int16, CallerTID int16, pad uint16,
//         Args [6]uint64, DataVA uint64, DataLen uint64}
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallDelegatedRecv(arg0, _, _, _, _, _ uint64) int64 {
	resultPtr := uintptr(arg0)

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}
	if resultPtr == 0 {
		return -14 // EFAULT
	}

	myPID := int16(callerPriest.PID)

	// Verify this priest handles at least one SysID
	hasAny := false
	for i := sysid.ID(0); i < sysid.NumIDs; i++ {
		if int16(atomic.LoadInt32(&syscallDelegates[i].pid)) == myPID {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return -22 // EINVAL — not a handler
	}

	// Check all queues for pending requests
	e := delegateQueuePopAnyForPriest(myPID)
	if e != nil {
		writeDelegateRecvResult(resultPtr, callerPriest.PageTableL0PA, e)
		serial.RawUARTPuts("d")
		return 0
	}

	// No request pending — record recv state on the priest and block
	_, callerTID := getCurrentThreadPIDAndTID()
	if int(myPID) < proc.MaxPriests {
		rs := &delegateRecvStates[myPID]
		rs.recvTID = callerTID
		rs.resultPtr = resultPtr
	}

	ctx := blockForDelegatedRecv()
	if ctx == 0 {
		if int(myPID) < proc.MaxPriests {
			delegateRecvStates[myPID].recvTID = -1
			delegateRecvStates[myPID].resultPtr = 0
		}
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	return 0
}

// SyscallDelegatedReply replies to a delegated syscall, unblocking the caller.
// For Read syscalls, copies data from the handler's data page back to the
// caller's original buffer before waking.
//
// arg0 = callerPID
// arg1 = callerTID
// arg2 = return value (int64: bytes written/read, or fd, or errno)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallDelegatedReply(arg0, arg1, arg2, _, _, _ uint64) int64 {
	callerPID := int16(arg0)
	callerTID := int16(arg1)
	returnVal := int64(arg2)

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}

	// Look up the in-flight call info
	if int(callerTID) < MaxDelegateThreads {
		info := &delegateCallInfos[callerTID]
		if info.InUse {
			// For Read: copy data from handler's page back to caller's buffer.
			// Linux semantics: if the copy faults at any point (even after
			// partial success), read() returns -EFAULT. A partial copy means
			// the caller's buffer was bogus.
			if info.SysID == sysid.Read && returnVal > 0 && info.DataPagePA != 0 {
				bytesToCopy := uint32(returnVal)
				if bytesToCopy > info.CallerBufLen {
					bytesToCopy = info.CallerBufLen
				}
				if bytesToCopy > 4096 {
					bytesToCopy = 4096
				}
				actual := copyDataPageToCaller(info.DataPagePA, info.CallerBufVA, info.CallerL0PA, bytesToCopy)
				if uint32(actual) < bytesToCopy {
					serial.RawUARTPuts("[DLG] unable to write to client buffer @0x")
					serial.RawUARTHex64(uint64(info.CallerBufVA))
					serial.RawUARTPuts(", only ")
					serial.RawUARTDecimal(uint64(actual))
					serial.RawUARTPuts(" of ")
					serial.RawUARTDecimal(uint64(bytesToCopy))
					serial.RawUARTPuts(" were written before a fault\r\n")
					returnVal = -14 // EFAULT
				}
			}

			// Reclaim the data page
			if info.DataPagePA != 0 {
				handlerPriest := proc.FindPriestByPID(proc.PriestId(info.HandlerPID))
				reclaimDataPage(info.DataPagePA, info.DataPageVA, info.HandlerPID, handlerPriest)
			}

			info.InUse = false
		}
	}

	serial.RawUARTPuts("R")

	// Wake the blocked caller thread with the return value
	wakeDelegateCallerThread(callerPID, int32(callerTID), returnVal)

	return 0
}

// copyDataPageToCaller copies bytes from a data page (by PA) into the caller's
// buffer (by VA, using the caller's page table).
// Returns the number of bytes actually copied (may be less than count on fault).
func copyDataPageToCaller(pagePA uintptr, callerBufVA uintptr, callerL0PA uintptr, count uint32) int {
	scratchVA := kmem.MapPAToKernelScratch(pagePA)
	if scratchVA == 0 {
		return 0
	}

	src := unsafe.Slice((*byte)(unsafe.Pointer(scratchVA)), count)
	return kmem.CopyToUserWithL0(callerBufVA, callerL0PA, src, int(count))
}

// writeDelegateRecvResult writes the delegate recv result to userspace.
func writeDelegateRecvResult(resultPtr uintptr, l0PA uintptr, e *DelegateQueueEntry) {
	// Result layout: SysID(2) + CallerPID(2) + CallerTID(2) + pad(2) +
	//                Args[6](48) + DataVA(8) + DataLen(8) = 72 bytes
	writeU16ToUser(resultPtr+0, uint16(e.SysID), l0PA)
	writeI16ToUser(resultPtr+2, int16(e.CallerPID), l0PA)
	writeI16ToUser(resultPtr+4, e.CallerTID, l0PA)
	writeU16ToUser(resultPtr+6, 0, l0PA)
	for i := 0; i < 6; i++ {
		writeU64ToUser(resultPtr+8+uintptr(i*8), e.Args[i], l0PA)
	}
	// DataVA points directly at the data (no header — data starts at offset 0)
	writeU64ToUser(resultPtr+56, e.DataVA, l0PA)
	writeU64ToUser(resultPtr+64, uint64(e.DataLen), l0PA)
}

// writeU16ToUser writes a uint16 to userspace memory via scratch mapping.
func writeU16ToUser(addr uintptr, val uint16, l0PA uintptr) {
	if kmem.WalkUserPageTableWithL0(addr, l0PA) == 0 {
		if !kmem.HandleUserPageFault(addr, 0) {
			return
		}
	}
	pa := kmem.WalkUserPageTableWithL0(addr, l0PA)
	if pa == 0 {
		return
	}
	scratchVA := kmem.MapPAToKernelScratch(pa &^ 0xFFF)
	if scratchVA == 0 {
		return
	}
	offset := addr & 0xFFF
	*(*uint16)(unsafe.Pointer(scratchVA + offset)) = val
}

// writeI16ToUser writes an int16 to userspace memory via scratch mapping.
func writeI16ToUser(addr uintptr, val int16, l0PA uintptr) {
	writeU16ToUser(addr, uint16(val), l0PA)
}

// Linkname bridge functions — implemented in kmazarin/kmazarin/ipc_bridge.go
//
//go:linkname blockForDelegatedSyscall main.BlockForDelegatedSyscall
func blockForDelegatedSyscall() uintptr

//go:linkname blockForDelegatedRecv main.BlockForDelegatedRecv
func blockForDelegatedRecv() uintptr

//go:linkname wakeDelegateThread main.WakeDelegateThread
func wakeDelegateThread(tid int32, returnVal int64)

//go:linkname wakeDelegateCallerThread main.WakeDelegateCallerThread
func wakeDelegateCallerThread(pid int16, tid int32, returnVal int64)
