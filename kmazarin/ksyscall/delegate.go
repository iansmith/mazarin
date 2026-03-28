package ksyscall

// delegate.go — Syscall delegation: shepherds register to handle specific syscalls.
//
// A shepherd registers for one or more SysIDs (Write, Read, Openat, Close, etc.).
// The kernel intercepts matching syscalls from other shepherds and forwards them.
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
	"mazzy/shared/constants"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

// MaxDelegateQueueDepth is the max queued delegation requests per SysID.
const MaxDelegateQueueDepth = 8

// MaxDelegateThreads matches the thread pool size since TIDs are used
// as direct indices into delegateCallInfos.
const MaxDelegateThreads = constants.ThreadPoolSize

// delegateHandler maps a SysID to the shepherd that handles it.
// pid is int32 (not int16) because RISC-V lr.w/sc.w atomics require
// 4-byte alignment. With int16, odd-indexed array elements would be
// at 2-byte boundaries, causing misaligned load traps (scause=4).
type delegateHandler struct {
	pid int32 // handler shepherd PID (-1 = unregistered)
}

// delegateRecvState tracks a handler shepherd's blocked recv thread.
// Per-shepherd (not per-SysID) so one recv loop handles all registered syscalls.
type delegateRecvState struct {
	recvTID   int16   // TID blocked in DelegatedRecv (-1 = none)
	resultPtr uintptr // VA of result struct in handler's address space
}

// DelegateQueueEntry is a queued delegated syscall waiting for the handler.
type DelegateQueueEntry struct {
	CallerSID  proc.ShepherdId
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
	HandlerSID   int16    // Handler shepherd PID (to unmap from)
	CallerSID    int16    // Caller shepherd PID (for cleanup on caller death)
	CallerBufVA  uintptr  // Caller's original buffer VA (Read: copy-back destination)
	CallerBufLen uint32   // Max bytes caller requested (Read: cap for copy-back)
	CallerL0PA   uintptr  // Caller's L0 page table PA (for copy-back)
	SysID        sysid.ID // Which syscall (determines copy direction)
	InUse        bool
}

// syscallDelegates maps SysID → handler shepherd PID.
var syscallDelegates [sysid.NumIDs]delegateHandler

// delegateRecvStates tracks recv state per handler shepherd (indexed by PID).
var delegateRecvStates [proc.MaxShepherds]delegateRecvState

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

// IsDelegated returns true if the given SysID has a handler shepherd registered,
// the handler has signaled Ready, and the caller is not the handler itself.
// Both registration (HandleSyscalls) and readiness (SetReady) are required
// before the kernel will forward syscalls to the handler.
//
//go:nosplit
func IsDelegated(id sysid.ID, callerSID int16) bool {
	if id == sysid.Invalid || id >= sysid.NumIDs {
		return false
	}
	hpid := atomic.LoadInt32(&syscallDelegates[id].pid)
	if hpid < 0 || int16(hpid) == callerSID {
		return false
	}
	return isDelegateReady(proc.ShepherdId(hpid))
}

// isDelegateReady checks if the handler shepherd has signaled Ready.
// Scans ShepherdListData since PID != array index.
func isDelegateReady(pid proc.ShepherdId) bool {
	for i := 0; i < proc.MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PID == pid {
			return atomic.LoadInt32(&proc.ShepherdListData[i].Ready) != 0
		}
	}
	return false
}

// DelegateSyscall forwards a syscall to the registered handler shepherd.
// Allocates a data page for syscalls that transfer data (Write, Read).
// Blocks the caller until the handler replies.
//
//go:noinline
func DelegateSyscall(id sysid.ID, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	handlerSID := int16(atomic.LoadInt32(&syscallDelegates[id].pid))
	if handlerSID < 0 {
		return -38 // ENOSYS
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	_, callerTID := getCurrentThreadSIDAndTID()

	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
	if handlerShepherd == nil {
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
			pa, va, n := allocAndCopyCallerData(handlerSID, handlerShepherd, uintptr(arg1), count)
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
			pa, va := allocEmptyDataPage(handlerSID, handlerShepherd)
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
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.LoadFile:
		// LoadFile: copy pathname string into data page (path is in arg0).
		// arg0 = pathname pointer, arg1 = result struct pointer (stashed in CallerBufVA)
		if arg0 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg0))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.ReadFilePages:
		// ReadFilePages: copy pathname string into data page (path is in arg0).
		// arg0 = pathname, arg1 = destVA, arg2 = destSize, arg3 = fileOffset, arg4 = readLen
		// CallerSID is available from DelegateQueueEntry.CallerSID for cross-shepherd DMA.
		if arg0 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg0))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	// --- File syscalls delegated to the linux shepherd ---

	case sysid.Mkdirat, sysid.Unlinkat, sysid.Fchmodat,
		sysid.Utimensat, sysid.Faccessat, sysid.Readlinkat,
		sysid.Statfs, sysid.Chdir:
		// String argument in arg1 (pathname).
		if arg1 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Renameat:
		// renameat2: arg1 = oldpath, arg3 = newpath. Copy oldpath; newpath
		// goes through args (handler reads it from a second delegation or
		// we pack both into one page separated by null).
		if arg1 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Fstatat:
		// fstatat: arg1 = pathname, arg2 = statbuf (output).
		// Copy pathname string in; reply path copies stat data back.
		if arg1 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Fstat, sysid.Getdents64, sysid.Getcwd,
		sysid.Fstatfs:
		// Output buffer syscalls: handler fills data page, kernel copies back.
		// Like Read: allocate empty page for handler to fill.
		if arg1 != 0 && arg2 > 0 {
			count := arg2
			if count > 4096 {
				count = 4096
			}
			pa, va := allocEmptyDataPage(handlerSID, handlerShepherd)
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(count)
		}

	default:
		// Lseek, Close, Fchdir, Ioctl, Ftruncate, Fsync, Fdatasync, etc:
		// no data page, just args and return value.
	}

	// Enqueue the request
	entry := DelegateQueueEntry{
		CallerSID:  callerShepherd.PID,
		CallerTID:  callerTID,
		SysID:      id,
		DataPagePA: dataPagePA,
		DataVA:     handlerDataVA,
		DataLen:    dataLen,
	}
	entry.Args = [6]uint64{arg0, arg1, arg2, arg3, arg4, arg5}

	// Disable IRQs around the queue push + recv state check to prevent
	// preemption mid-operation, which could corrupt the ring buffer or
	// cause a TOCTOU race on delegateRecvStates.
	savedDAIF := saveAndDisableIRQs()

	q := &delegateQueues[id]
	next := (q.tail + 1) % MaxDelegateQueueDepth
	if next == q.head {
		restoreIRQs(savedDAIF)
		reclaimDataPage(dataPagePA, handlerDataVA, handlerSID, handlerShepherd)
		serial.RawUARTPuts("[DLG] queue full\r\n")
		return -11 // EAGAIN
	}
	q.entries[q.tail] = entry
	q.tail = next

	// Stash call info for the reply path (indexed by caller TID).
	// CallerBufVA/CallerBufLen identify the output buffer for copy-back syscalls.
	// Default: arg1=buf, arg2=count (matches read, getdents64, etc.).
	callerBufVA := uintptr(arg1)
	callerBufLen := uint32(arg2)
	switch id {
	case sysid.Fstat:
		// fstat(fd, statbuf): arg1=statbuf, no size arg — use struct_stat size.
		callerBufLen = 128 // sizeof(struct stat) on linux/arm64 and linux/amd64
	case sysid.Fstatfs:
		// fstatfs(fd, buf): arg1=buf, no size arg.
		callerBufLen = 120 // sizeof(struct statfs) on linux
	case sysid.Fstatat:
		// fstatat(dirfd, path, statbuf, flags): arg2=statbuf (output).
		callerBufVA = uintptr(arg2)
		callerBufLen = 128
	case sysid.Getcwd:
		// getcwd(buf, size): arg0=buf, arg1=size.
		callerBufVA = uintptr(arg0)
		callerBufLen = uint32(arg1)
	case sysid.Readlinkat:
		// readlinkat(dirfd, path, buf, bufsiz): arg2=buf, arg3=bufsiz.
		callerBufVA = uintptr(arg2)
		callerBufLen = uint32(arg3)
	}
	if int(callerTID) < MaxDelegateThreads {
		info := &delegateCallInfos[callerTID]
		info.DataPagePA = dataPagePA
		info.DataPageVA = handlerDataVA
		info.HandlerSID = handlerSID
		info.CallerSID = int16(callerShepherd.PID)
		info.CallerBufVA = callerBufVA
		info.CallerBufLen = callerBufLen
		info.CallerL0PA = callerShepherd.PageTableL0PA
		info.SysID = id
		info.InUse = true
	}

	// If handler has a thread blocked in recv, wake it
	if handlerSID >= 0 && int(handlerSID) < proc.MaxShepherds {
		rs := &delegateRecvStates[handlerSID]
		if rs.recvTID >= 0 {
			recvTID := rs.recvTID
			resultPtr := rs.resultPtr
			rs.recvTID = -1
			rs.resultPtr = 0

			// Dequeue and deliver to the handler
			e := delegateQueuePop(id)
			if e != nil {
				if !writeDelegateRecvResult(resultPtr, handlerShepherd.PageTableL0PA, e) {
					serial.RawUARTPuts("[DLG] recv result write fault\r\n")
				}
				serial.RawUARTPuts("[DLG:deliver] recvTID=")
				serial.RawUARTDecimal(uint64(recvTID))
				serial.RawUARTPuts(" sysID=")
				serial.RawUARTDecimal(uint64(id))
				serial.RawUARTPuts("\r\n")
				wakeDelegateThread(int32(recvTID), 0)
			}
		} else {
			serial.RawUARTPuts("[DLG:queued] handlerSID=")
			serial.RawUARTDecimal(uint64(handlerSID))
			serial.RawUARTPuts(" sysID=")
			serial.RawUARTDecimal(uint64(id))
			serial.RawUARTPuts(" recvTID=-1\r\n")
		}
	}

	restoreIRQs(savedDAIF)

	// Block the caller
	serial.RawUARTPuts("[DLG:block] callerTID=")
	serial.RawUARTDecimal(uint64(callerTID))
	serial.RawUARTPuts(" sysID=")
	serial.RawUARTDecimal(uint64(id))
	serial.RawUARTPuts("\r\n")
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		serial.RawUARTPuts("[DLG:block] NO NEXT THREAD, EAGAIN\r\n")
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	// Return value is set by the reply handler (WakeDelegateCallerThread)
	return 0
}

// allocAndCopyCallerData allocates a page, copies caller data into it, and
// maps it into the handler's address space. Returns (PA, handlerVA, bytesCopied).
func allocAndCopyCallerData(handlerSID int16, handlerShepherd *proc.Shepherd, callerBufVA uintptr, count uint64) (uintptr, uint64, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerSID)
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

	va := bumpAllocForShepherd(handlerShepherd, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	kmem.MapPageInProcess(handlerSID, uintptr(va), pa, 0) // RW
	handlerShepherd.Spans.Add(va, 4096)

	return pa, va, count
}

// allocEmptyDataPage allocates a zeroed page and maps it into the handler's space.
// The handler will fill it with data (for Read).
func allocEmptyDataPage(handlerSID int16, handlerShepherd *proc.Shepherd) (uintptr, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerSID)
	if pa == 0 {
		return 0, 0
	}

	scratchVA := kmem.MapPAToKernelScratch(pa)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0
	}
	zeroPage(scratchVA)

	va := bumpAllocForShepherd(handlerShepherd, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0
	}
	kmem.MapPageInProcess(handlerSID, uintptr(va), pa, 0) // RW
	handlerShepherd.Spans.Add(va, 4096)

	return pa, va
}

// allocAndCopyCallerString allocates a data page and copies a null-terminated
// string from the caller's address space into it. Copies up to the end of the
// source page to avoid crossing into potentially unmapped memory.
// The data page is pre-zeroed, guaranteeing null termination.
// Returns (PA, handlerVA, stringLength).
func allocAndCopyCallerString(handlerSID int16, handlerShepherd *proc.Shepherd, callerStrVA uintptr) (uintptr, uint64, uint64) {
	pa := kmem.AllocPage(kmem.PageSharedIPC, handlerSID)
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

	va := bumpAllocForShepherd(handlerShepherd, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	kmem.MapPageInProcess(handlerSID, uintptr(va), pa, 0) // RW
	handlerShepherd.Spans.Add(va, 4096)

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
func reclaimDataPage(pa uintptr, handlerVA uint64, handlerSID int16, handlerShepherd *proc.Shepherd) {
	if pa == 0 {
		return
	}
	if handlerVA != 0 && handlerShepherd != nil {
		kmem.UnmapUserPageWithL0(uintptr(handlerVA), handlerShepherd.PageTableL0PA)
		handlerShepherd.Spans.Remove(handlerVA, 4096)
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

// delegateQueuePopAnyForShepherd scans all SysID queues for entries targeting
// the given handler shepherd. Returns the first found, or nil.
func delegateQueuePopAnyForShepherd(handlerSID int16) *DelegateQueueEntry {
	for i := sysid.ID(0); i < sysid.NumIDs; i++ {
		hpid := int16(atomic.LoadInt32(&syscallDelegates[i].pid))
		if hpid != handlerSID {
			continue
		}
		if e := delegateQueuePop(i); e != nil {
			return e
		}
	}
	return nil
}

// SyscallRegisterSyscallHandler registers the calling shepherd as the handler
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

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}

	h := &syscallDelegates[id]
	if !atomic.CompareAndSwapInt32(&h.pid, -1, int32(callerShepherd.PID)) {
		return -16 // EBUSY — already registered
	}

	serial.RawUARTPuts("[DLG] P")
	serial.RawUARTDecimal(uint64(callerShepherd.PID))
	serial.RawUARTPuts(" handles SysID ")
	serial.RawUARTDecimal(uint64(id))
	serial.RawUARTPuts("\r\n")

	return 0
}

// SyscallDelegatedRecv blocks until a delegated syscall request arrives
// for any SysID this shepherd handles.
//
// arg0 = pointer to DelegateRecvResult struct in userspace:
//        {SysID uint16, CallerSID int16, CallerTID int16, pad uint16,
//         Args [6]uint64, DataVA uint64, DataLen uint64}
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallDelegatedRecv(arg0, _, _, _, _, _ uint64) int64 {
	resultPtr := uintptr(arg0)

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	if resultPtr == 0 {
		return -14 // EFAULT
	}

	mySID := int16(callerShepherd.PID)

	// Verify this shepherd handles at least one SysID
	hasAny := false
	for i := sysid.ID(0); i < sysid.NumIDs; i++ {
		if int16(atomic.LoadInt32(&syscallDelegates[i].pid)) == mySID {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return -22 // EINVAL — not a handler
	}

	// Disable IRQs around the queue pop + recv state set to prevent
	// preemption between checking the queue and recording the recv state.
	// Without this, a concurrent DelegateSyscall could enqueue + check
	// recvTID between our pop (empty) and our recvTID set, losing the request.
	savedDAIF := saveAndDisableIRQs()

	// Check all queues for pending requests
	e := delegateQueuePopAnyForShepherd(mySID)
	if e != nil {
		restoreIRQs(savedDAIF)
		serial.RawUARTPuts("[DLG:recv-hit] SID=")
		serial.RawUARTDecimal(uint64(mySID))
		serial.RawUARTPuts(" sysID=")
		serial.RawUARTDecimal(uint64(e.SysID))
		serial.RawUARTPuts("\r\n")
		if !writeDelegateRecvResult(resultPtr, callerShepherd.PageTableL0PA, e) {
			return -14 // EFAULT — handler's result buffer is not mapped
		}
		return 0
	}

	// No request pending — record recv state on the shepherd and block
	_, callerTID := getCurrentThreadSIDAndTID()
	if int(mySID) < proc.MaxShepherds {
		rs := &delegateRecvStates[mySID]
		rs.recvTID = callerTID
		rs.resultPtr = resultPtr
	}

	restoreIRQs(savedDAIF)

	serial.RawUARTPuts("[DLG:recv-block] SID=")
	serial.RawUARTDecimal(uint64(mySID))
	serial.RawUARTPuts(" TID=")
	serial.RawUARTDecimal(uint64(callerTID))
	serial.RawUARTPuts("\r\n")

	ctx := blockForDelegatedRecv()
	if ctx == 0 {
		if int(mySID) < proc.MaxShepherds {
			delegateRecvStates[mySID].recvTID = -1
			delegateRecvStates[mySID].resultPtr = 0
		}
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	return 0
}

// SyscallReply replies to a delegated syscall, unblocking the caller.
// For Read syscalls, copies data from the handler's data page back to the
// caller's original buffer before waking.
//
// arg0 = callerSID
// arg1 = callerTID
// arg2 = return value (int64: bytes written/read, or fd, or errno)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallReply(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	callerSID := int16(arg0)
	callerTID := int16(arg1)
	returnVal := int64(arg2)

	replyingShepherd := proc.CurrentShepherd()
	if replyingShepherd == nil {
		return -1 // EPERM
	}

	// Look up the in-flight call info
	if int(callerTID) < MaxDelegateThreads {
		info := &delegateCallInfos[callerTID]
		if info.InUse {
			// Verify the replying shepherd is the registered handler for this
			// delegation. Without this check, any shepherd that guesses a
			// caller's TID could forge a reply with an arbitrary return value.
			if info.HandlerSID != int16(replyingShepherd.PID) {
				return -1 // EPERM
			}
			// For Read: copy data from handler's page back to caller's buffer.
			// Linux semantics: if the copy faults at any point (even after
			// partial success), read() returns -EFAULT. A partial copy means
			// the caller's buffer was bogus.
			if isCopyBackSyscall(info.SysID) && returnVal > 0 && info.DataPagePA != 0 {
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

			// For LoadFile: write result struct to caller's address space.
			// arg3 = targetVA, arg4 = numPages, arg5 = bytesRead.
			// CallerBufVA stores the result struct pointer for LoadFile.
			if info.SysID == sysid.LoadFile && returnVal >= 0 && info.CallerBufVA != 0 {
				if !writeU64ToUserChecked(info.CallerBufVA, arg3, info.CallerL0PA) ||
					!writeU64ToUserChecked(info.CallerBufVA+8, arg4, info.CallerL0PA) ||
					!writeU64ToUserChecked(info.CallerBufVA+16, arg5, info.CallerL0PA) {
					returnVal = -14 // EFAULT
				}
			}

			// Reclaim the data page
			if info.DataPagePA != 0 {
				handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(info.HandlerSID))
				reclaimDataPage(info.DataPagePA, info.DataPageVA, info.HandlerSID, handlerShepherd)
			}

			info.InUse = false
		}
	}

	// Wake the blocked caller thread with the return value
	serial.RawUARTPuts("[DLG:reply] callerSID=")
	serial.RawUARTDecimal(uint64(callerSID))
	serial.RawUARTPuts(" callerTID=")
	serial.RawUARTDecimal(uint64(callerTID))
	serial.RawUARTPuts(" ret=")
	serial.RawUARTHexCompact(uint64(returnVal))
	serial.RawUARTPuts("\r\n")
	wakeDelegateCallerThread(callerSID, int32(callerTID), returnVal)

	return 0
}

// isCopyBackSyscall returns true if the given syscall ID uses the Read pattern:
// handler fills a data page and the kernel copies the result back to the caller.
func isCopyBackSyscall(id sysid.ID) bool {
	switch id {
	case sysid.Read, sysid.Fstat, sysid.Getdents64, sysid.Getcwd,
		sysid.Fstatfs, sysid.Fstatat, sysid.Readlinkat, sysid.Readv:
		return true
	}
	return false
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
// Returns false if any write faults — the result struct is partially written
// and the caller should return -EFAULT.
func writeDelegateRecvResult(resultPtr uintptr, l0PA uintptr, e *DelegateQueueEntry) bool {
	// Result layout: SysID(2) + CallerSID(2) + CallerTID(2) + pad(2) +
	//                Args[6](48) + DataVA(8) + DataLen(8) = 72 bytes
	if !writeU16ToUser(resultPtr+0, uint16(e.SysID), l0PA) {
		return false
	}
	if !writeI16ToUser(resultPtr+2, int16(e.CallerSID), l0PA) {
		return false
	}
	if !writeI16ToUser(resultPtr+4, e.CallerTID, l0PA) {
		return false
	}
	if !writeU16ToUser(resultPtr+6, 0, l0PA) {
		return false
	}
	for i := 0; i < 6; i++ {
		if !writeU64ToUserChecked(resultPtr+8+uintptr(i*8), e.Args[i], l0PA) {
			return false
		}
	}
	// DataVA points directly at the data (no header — data starts at offset 0)
	if !writeU64ToUserChecked(resultPtr+56, e.DataVA, l0PA) {
		return false
	}
	if !writeU64ToUserChecked(resultPtr+64, uint64(e.DataLen), l0PA) {
		return false
	}
	return true
}

// writeU16ToUser writes a uint16 to userspace memory via scratch mapping.
// Returns false if the page is not mapped (no demand-faulting — would use
// the wrong address space in cross-process contexts).
func writeU16ToUser(addr uintptr, val uint16, l0PA uintptr) bool {
	pa := kmem.WalkUserPageTableWithL0(addr, l0PA)
	if pa == 0 {
		return false
	}
	scratchVA := kmem.MapPAToKernelScratch(pa &^ 0xFFF)
	if scratchVA == 0 {
		return false
	}
	offset := addr & 0xFFF
	*(*uint16)(unsafe.Pointer(scratchVA + offset)) = val
	return true
}

// writeI16ToUser writes an int16 to userspace memory via scratch mapping.
func writeI16ToUser(addr uintptr, val int16, l0PA uintptr) bool {
	return writeU16ToUser(addr, uint16(val), l0PA)
}

// writeU64ToUserChecked writes a uint64 to userspace memory via scratch mapping.
// Returns false if the page is not mapped. Used by writeDelegateRecvResult
// where error propagation is needed.
func writeU64ToUserChecked(userVA uintptr, val uint64, l0PA uintptr) bool {
	pa := kmem.WalkUserPageTableWithL0(userVA, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := kmem.MapPAToKernelScratch(pa)
	if kernelVA == 0 {
		return false
	}
	*(*uint64)(unsafe.Pointer(kernelVA)) = val
	kmem.CleanPageCache(kernelVA)
	return true
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

// CleanupDelegateForDeadShepherd reclaims resources for a shepherd that is being terminated.
// Handles both cases:
//   - Dying shepherd was a CALLER: reclaim data pages from in-flight and queued delegations.
//   - Dying shepherd was a HANDLER: unregister SysIDs, clear recv state.
//
// Called from terminateShepherdImpl with schedulerLock held and IRQs disabled.
// Returns the number of in-flight delegations where the dying shepherd was the
// HANDLER — the caller (terminateShepherdImpl) must wake those blocked caller
// threads. The TIDs are written to DelegateOrphanedCallerTIDs.
func CleanupDelegateForDeadShepherd(pid int16) int {
	orphanCount := 0

	// Part 1: Dying shepherd was a CALLER — reclaim in-flight data pages.
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if !info.InUse || info.CallerSID != pid {
			continue
		}
		// Reclaim the data page (owned by handler, mapped in handler's space)
		if info.DataPagePA != 0 {
			handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(info.HandlerSID))
			reclaimDataPage(info.DataPagePA, info.DataPageVA, info.HandlerSID, handlerShepherd)
		}
		info.InUse = false
	}

	// Part 2: Dying shepherd was a CALLER — neuter queued entries.
	// Zero out DataPagePA so the handler won't double-free when it dequeues.
	for sid := sysid.ID(0); sid < sysid.NumIDs; sid++ {
		q := &delegateQueues[sid]
		for idx := q.head; idx != q.tail; idx = (idx + 1) % MaxDelegateQueueDepth {
			e := &q.entries[idx]
			if int16(e.CallerSID) != pid {
				continue
			}
			if e.DataPagePA != 0 {
				hpid := int16(atomic.LoadInt32(&syscallDelegates[sid].pid))
				handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(hpid))
				reclaimDataPage(e.DataPagePA, e.DataVA, hpid, handlerShepherd)
				e.DataPagePA = 0
				e.DataVA = 0
			}
		}
	}

	// Part 3: Dying shepherd was a HANDLER — unregister all SysIDs.
	for sid := sysid.ID(0); sid < sysid.NumIDs; sid++ {
		if int16(atomic.LoadInt32(&syscallDelegates[sid].pid)) == pid {
			atomic.StoreInt32(&syscallDelegates[sid].pid, -1)
		}
	}

	// Part 4: Dying shepherd was a HANDLER — clear recv state.
	if int(pid) < proc.MaxShepherds {
		delegateRecvStates[pid].recvTID = -1
		delegateRecvStates[pid].resultPtr = 0
	}

	// Part 5: Dying shepherd was a HANDLER — find orphaned callers.
	// Data pages are owned by the dying handler and will be freed by
	// CleanupShepherdPages. Clear InUse and record caller TIDs to wake.
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if !info.InUse || info.HandlerSID != pid {
			continue
		}
		info.DataPagePA = 0 // Will be freed by CleanupShepherdPages
		info.DataPageVA = 0
		info.InUse = false
		if orphanCount < len(DelegateOrphanedCallerTIDs) {
			DelegateOrphanedCallerTIDs[orphanCount] = int16(i)
			DelegateOrphanedCallerSIDs[orphanCount] = info.CallerSID
			orphanCount++
		}
	}

	return orphanCount
}

// DelegateOrphanedCallerTIDs holds TIDs of callers whose handler shepherd died.
// DelegateOrphanedCallerSIDs holds the corresponding caller PIDs.
// Written by CleanupDelegateForDeadShepherd, read by TerminateShepherd.
var DelegateOrphanedCallerTIDs [MaxDelegateThreads]int16
var DelegateOrphanedCallerSIDs [MaxDelegateThreads]int16
