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
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/constants"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

// MaxDelegateThreads matches the thread pool size since TIDs are used
// as direct indices into delegateCallInfos.
const MaxDelegateThreads = constants.ThreadPoolSize

// delegateHandler maps a SysID to the shepherd that handles it.
// pid is int32 (not int16) because RISC-V lr.w/sc.w atomics require
// 4-byte alignment. With int16, odd-indexed array elements would be
// at 2-byte boundaries, causing misaligned load traps (scause=4).
type delegateHandler struct {
	pid     int32 // handler shepherd PID (-1 = unregistered)
	ringIdx uint8 // which ring on the handler to send delegate requests to
}

// DelegateCallInfo records per-caller-thread state while a delegated syscall
// is in flight. Used by the reply path to copy data back (Read) and reclaim pages.
type DelegateCallInfo struct {
	DataPagePA      uintptr  // PA of data page (0 = no page)
	DataPageVA      uint64   // VA of data page in handler's address space
	HandlerSID      int16    // Handler shepherd PID (to unmap from)
	CallerSID       int16    // Caller shepherd PID (for cleanup on caller death)
	CallerBufVA     uintptr  // Caller's original buffer VA (Read: copy-back destination)
	CallerBufLen    uint32   // Max bytes caller requested (Read: cap for copy-back)
	CallerL0PA      uintptr  // Caller's L0 page table PA (for copy-back)
	SysID           sysid.ID // Which syscall (determines copy direction)
	InUse           bool
	WritableMapping bool // MmapPageFill: map page RW instead of RO

	// CallerDead is set by CleanupDelegateForDeadShepherd when the caller
	// shepherd dies while its delegate request is still queued in the
	// handler's uring ring. We can't unmap the handler's data page at
	// cleanup time — the handler may still dereference it when it pops the
	// msg. Instead we leave page + InUse intact and tell Reply that the
	// caller is gone, so Reply still reclaims the page but skips the wake.
	CallerDead bool

	// MmapPageFlush round state — used by the reply path to continue
	// sending rounds if the response page was full (count == 511).
	FlushResponsePA uintptr // PA of response page (kernel owns)
	FlushResponseVA uint64  // VA of response page in handler's address space
	FlushFD         uint64  // fd for the file being flushed
	FlushCallerSID  int16   // SID of the shepherd whose pages are being flushed
	// FlushOffset/FlushLength constrain the flush to a file-offset range so
	// partial munmap doesn't drop pages outside the unmapped region. Length=0
	// means "all" (death cleanup).
	FlushOffset uint64
	FlushLength uint64
}


// stdioWriteRingIdx — kernel-side override for pipe-buffered Write
// delegation. When set, fd<=2 Writes are sent on this ring index of
// the registered Write handler instead of the handler's default ring
// (set via RegisterSyscallHandler). The override exists so stdio
// backpressure can't starve the blocking-delegate ring (or vice versa)
// when both share the same handler. -1 = unset (no override; pipe
// Writes use the default ring of the Write handler).
//
// Set via SyscallRegisterStdioWriteRing. Read on every Write delegate.
// int32 (not uint8) for atomic alignment; values ≤ MaxRingsPerShepherd.
var stdioWriteRingIdx int32 = -1

// syscallDelegates maps SysID → handler shepherd PID.
var syscallDelegates [sysid.NumIDs]delegateHandler

// delegateCallInfos tracks in-flight delegated calls (indexed by caller TID).
var delegateCallInfos [MaxDelegateThreads]DelegateCallInfo

func init() {
	for i := range syscallDelegates {
		syscallDelegates[i].pid = -1
	}
}

// LinuxDelegateSID returns the SID of the shepherd currently registered as the
// handler for write() — which is the linux shepherd. Returns -1 if none registered.
// Used by KernelIdleLoop to target idle flush hints.
//
//go:nosplit
func LinuxDelegateSID() int16 {
	return int16(atomic.LoadInt32(&syscallDelegates[sysid.Write].pid))
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
	case sysid.Write, sysid.Pwrite64:
		// Write/Pwrite64: copy caller's buffer into data page
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

	case sysid.Read, sysid.Pread64:
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
		sysid.Utimensat, sysid.Faccessat, sysid.Readlinkat:
		// *at-family syscalls take dirfd in arg0 and pathname in arg1.
		if arg1 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Chdir:
		// chdir(path): pathname is in arg0 (not arg1 — chdir is not an *at
		// syscall). Without this case the handler would see an empty path
		// and return EINVAL.
		if arg0 != 0 {
			pa, va, n := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg0))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(n)
		}

	case sysid.Statfs:
		// statfs(path, buf): pathname in arg0, output buf in arg1.
		// Copy pathname in; handler reuses the same page to write the
		// 120-byte struct statfs64 back. Kernel copies it to arg1 via the
		// isCopyBackSyscall path.
		if arg0 != 0 {
			pa, va, _ := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg0))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = 4096
		}

	case sysid.Renameat:
		// renameat2: arg1 = oldpath, arg3 = newpath. Pack both paths
		// into one data page as "oldpath\0newpath\0". The handler reads
		// the old path from offset 0 and the new path from the byte
		// after the first null. Arg0 stores the length of oldpath+null
		// so the handler can find the split point.
		if arg1 != 0 && arg3 != 0 {
			pa, va, oldLen := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			// Append newpath after oldpath+null in the same page.
			scratchVA := kmem.MapPAToKernelScratch(pa)
			newOff := oldLen + 1 // skip past null terminator
			maxNew := uintptr(4096) - uintptr(newOff)
			newPageOff := uintptr(arg3) & 0xFFF
			maxCopy := uintptr(4096) - newPageOff
			if maxCopy > maxNew {
				maxCopy = maxNew
			}
			dst := unsafe.Slice((*byte)(unsafe.Pointer(scratchVA+uintptr(newOff))), maxCopy)
			kmem.CopyFromUser(dst, uintptr(arg3), int(maxCopy))
			// Find newpath length.
			var newLen uint64
			for i := uintptr(0); i < maxCopy; i++ {
				if dst[i] == 0 {
					newLen = uint64(i)
					break
				}
			}
			if newLen == 0 && maxCopy > 0 && dst[0] != 0 {
				newLen = uint64(maxCopy)
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = uint32(newOff + newLen + 1) // total: oldpath\0newpath\0
			// Store oldpath+null length in arg0 so handler can split.
			arg0 = newOff
		}

	case sysid.Fstatat:
		// fstatat: arg1 = pathname, arg2 = statbuf (output).
		// Copy pathname string in; handler reuses page for stat output.
		// dataLen must cover the full page so the handler can write 128
		// bytes of struct stat back into DataBuf().
		if arg1 != 0 {
			pa, va, _ := allocAndCopyCallerString(handlerSID, handlerShepherd, uintptr(arg1))
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = 4096
		}

	case sysid.Fstat:
		// fstat(fd, statbuf): arg0=fd, arg1=statbuf. No count arg.
		// Always allocate a data page (handler writes 128-byte stat result).
		if arg1 != 0 {
			pa, va := allocEmptyDataPage(handlerSID, handlerShepherd)
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = 4096
		}

	case sysid.Fstatfs:
		// fstatfs(fd, buf): arg0=fd, arg1=buf. No count arg.
		if arg1 != 0 {
			pa, va := allocEmptyDataPage(handlerSID, handlerShepherd)
			if pa == 0 {
				return -12 // ENOMEM
			}
			dataPagePA = pa
			handlerDataVA = va
			dataLen = 4096
		}

	case sysid.Getdents64, sysid.Getcwd:
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
		// Lseek, Close, Fchdir, Ioctl, Ftruncate, Fsync, Fdatasync, Flock, etc:
		// no data page, just args and return value.
	}

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
		callerBufLen = 120
	case sysid.Statfs:
		// statfs(path, buf): arg1=buf, no size arg.
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
	// Pre-fault the caller's output buffer pages while we're still in the
	// caller's context (TTBR0 + CurrentShepherd() are correct). During the
	// reply path, CopyToUserWithL0 walks the caller's page table with an
	// explicit L0 PA and cannot demand-fault missing pages (wrong TTBR0
	// context). Without this, stat/read/getdents on stack buffers that
	// haven't been touched yet will EFAULT.
	if isCopyBackSyscall(id) && callerBufVA != 0 && callerBufLen > 0 {
		prefaultOutputBuffer(callerBufVA, uintptr(callerBufLen))
	}

	// Pipe-buffered Write delegation: on Linux, write(fd=1/2) to a stdout pipe
	// returns near-instantly once the bytes land in the pipe buffer — the
	// reader consumes asynchronously. Mazzy's synchronous delegate-and-block
	// pattern would turn the same call into a cross-process IPC round-trip
	// that can close deadlock cycles (fs fmt.Printf inside a LoadFile handler
	// → linux Write delegate → linux waiting on the handler that's waiting
	// for fs). For stdio Write we therefore:
	//   - Skip delegateCallInfos InUse tracking (no Reply expected; the
	//     handler will free the data page via SysReleaseDelegatePage).
	//   - Skip blockForDelegatedSyscall; return the byte count immediately.
	// All other delegated syscalls (incl. write to fd>2) keep the original
	// block-until-reply semantics, because the handler must report the
	// actual number of bytes written by the underlying file system and the
	// kernel must reclaim the data page on Reply rather than relying on the
	// handler to do so.
	//
	// IMPORTANT: pipe-buffering is gated on fd<=2 so it only applies to the
	// linux shepherd's stdout/stderr lane (which calls SysReleaseDelegatePage).
	// If we pipe-buffered fd>2 Writes too, the linux file lane's req.Reply()
	// would arrive at the kernel as a stale SyscallReply targeting whatever
	// delegate the caller TID is currently blocked on — unmapping that
	// (unrelated) syscall's data page and corrupting its return value.
	pipeBuffered := id == sysid.Write && arg0 <= 2
	if !pipeBuffered && int(callerTID) < MaxDelegateThreads {
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

	// Send the request to the handler's uring ring.
	reqPayload := ipc.FSDelegateReqPayload{
		SysID:     uint16(id),
		CallerSID: int16(callerShepherd.PID),
		CallerTID: callerTID,
		Args:      [6]uint64{arg0, arg1, arg2, arg3, arg4, arg5},
		DataVA:    handlerDataVA,
		DataLen:   dataLen,
	}
	msg := ipc.EncodeFSDelegateReq(&reqPayload)
	// Pipe-buffered Writes (fd<=2) get routed to the stdio override ring
	// when one is registered. Same handler shepherd, different ring —
	// the kernel-half of the "stdio on its own ring" backpressure split.
	handlerRingIdx := syscallDelegates[id].ringIdx
	if pipeBuffered {
		if r := atomic.LoadInt32(&stdioWriteRingIdx); r >= 0 {
			handlerRingIdx = uint8(r)
		}
	}
	result, _ := uringSendKernel(-1, handlerSID, handlerRingIdx, uintptr(unsafe.Pointer(&msg)))
	if result < 0 {
		// Ring full or target gone — reclaim and fail.
		if !pipeBuffered && int(callerTID) < MaxDelegateThreads {
			delegateCallInfos[callerTID].InUse = false
		}
		reclaimDataPage(dataPagePA, handlerDataVA, handlerSID, handlerShepherd)
		klog.Errf("[DLG] uring send failed sysid=%d handler=%d ring=%d\n",
			uint32(id), int32(handlerSID), uint32(handlerRingIdx))
		return -11 // EAGAIN
	}

	if pipeBuffered {
		// Linux-pipe semantics: bytes are in the handler's ring buffer
		// (uring queue); return bytes-written count to caller without
		// blocking. Handler consumes asynchronously.
		return int64(dataLen)
	}

	// Block the caller until the handler replies via SyscallReply.
	RecordDelegateBlock(uint16(id))
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		klog.Errf("[DLG:block] NO NEXT THREAD, EAGAIN\n")
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
	if !kmem.MapPageInProcess(handlerSID, uintptr(va), pa, 0) { // RW
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
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

	// Copy the caller's null-terminated string into our scratch page.
	// Two-phase to avoid faulting on an unmapped successor page when the
	// string ends well inside its current page:
	//   phase 1 — copy up to the end of the caller's current page; this is
	//             always safe since we already faulted on it implicitly via
	//             the syscall path.
	//   phase 2 — if no null was found in phase 1, the string crosses a
	//             page boundary. Continue copying into the rest of the
	//             scratch page; CopyFromUser walks pages and returns false
	//             on any unmapped page, in which case we report an error.
	//
	// Without phase 2 we silently truncated paths whose first null sits on
	// the next page, producing a partial path string at the IPC layer.
	// That manifested as bleve's `os.OpenFile(".../seg.zap", ...)` arriving
	// at fs.maz as `/tmp/fti-N/bleve` (the prefix), which then resolved to
	// the bleve directory's inode and produced spurious EISDIR/ENOENT.
	dst := unsafe.Slice((*byte)(unsafe.Pointer(scratchVA)), 4096)

	firstChunk := uintptr(4096) - (callerStrVA & 0xFFF)
	if firstChunk > 4096 {
		firstChunk = 4096
	}
	if !kmem.CopyFromUser(dst[:firstChunk], callerStrVA, int(firstChunk)) {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}

	// Find the null terminator to determine actual string length.
	var strLen uint64
	hasNull := false
	for i := uintptr(0); i < firstChunk; i++ {
		if dst[i] == 0 {
			strLen = uint64(i)
			hasNull = true
			break
		}
	}

	// Phase 2: string spans into the next page. Copy more, but bound to
	// the scratch page (4 KB cap on path length, matching PATH_MAX-ish).
	if !hasNull && firstChunk < 4096 {
		secondChunk := uintptr(4096) - firstChunk
		if !kmem.CopyFromUser(dst[firstChunk:firstChunk+secondChunk], callerStrVA+firstChunk, int(secondChunk)) {
			// Successor page unmapped — caller passed a string that runs
			// off the end of mapped memory without a terminator.
			kmem.ReleasePageByPA(pa)
			return 0, 0, 0
		}
		for i := firstChunk; i < firstChunk+secondChunk; i++ {
			if dst[i] == 0 {
				strLen = uint64(i)
				hasNull = true
				break
			}
		}
	}

	if !hasNull {
		// String exceeded scratch page (≥4 KB without a null) — reject
		// rather than passing an unterminated buffer.
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}

	va := bumpAllocForShepherd(handlerShepherd, 4096)
	if va == 0 {
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	if !kmem.MapPageInProcess(handlerSID, uintptr(va), pa, 0) { // RW
		kmem.ReleasePageByPA(pa)
		return 0, 0, 0
	}
	handlerShepherd.Spans.Add(va, 4096)

	return pa, va, strLen
}

var allocStrDbg int

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

// SyscallReleaseDelegatePage unmaps and frees a data page that was mapped into
// the calling handler's address space for a pipe-buffered Write delegate (the
// caller did not block waiting for Reply, so the delegate-call-info tracking
// that reclaimDataPage normally drives doesn't apply). The handler calls this
// after it has consumed the bytes from the page.
//
// arg0 = handler VA (must be page-aligned, from SyscallRequest.DataVA())
// arg1 = number of pages (typically 1; Write max payload is 4096 bytes)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallReleaseDelegatePage(arg0, arg1, _, _, _, _ uint64) int64 {
	va := uintptr(arg0)
	numPages := int(arg1)
	if va == 0 || numPages <= 0 {
		return -22 // EINVAL
	}
	if va&0xFFF != 0 {
		return -22 // EINVAL — not page-aligned
	}

	handler := proc.CurrentShepherd()
	if handler == nil {
		return -1 // EPERM
	}

	for i := 0; i < numPages; i++ {
		pageVA := va + uintptr(i)*4096
		pa := kmem.WalkUserPageTableWithL0(pageVA, handler.PageTableL0PA)
		if pa == 0 {
			continue // already released or never mapped
		}
		kmem.UnmapUserPageWithL0(pageVA, handler.PageTableL0PA)
		handler.Spans.Remove(uint64(pageVA), 4096)
		kmem.ReleasePageByPA(pa)
	}
	return 0
}


// SyscallRegisterSyscallHandler registers the calling shepherd as the handler
// for a specific SysID. Can be called multiple times for different SysIDs.
//
// arg0 = SysID to handle
// arg1 = ring index on this shepherd to receive delegate requests (0 = default)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterSyscallHandler(arg0, arg1, _, _, _, _ uint64) int64 {
	id := sysid.ID(arg0)
	if id == sysid.Invalid || id >= sysid.NumIDs {
		return -22 // EINVAL
	}

	ringIdx := uint8(arg1)
	if ringIdx >= ipc.MaxRingsPerShepherd {
		return -22 // EINVAL — invalid ring index
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}

	h := &syscallDelegates[id]
	if !atomic.CompareAndSwapInt32(&h.pid, -1, int32(callerShepherd.PID)) {
		return -16 // EBUSY — already registered
	}
	h.ringIdx = ringIdx

	return 0
}

// SyscallRegisterStdioWriteRing sets the kernel-side ring index that
// pipe-buffered Writes (Write fd<=2) are sent to. The handler shepherd
// is whichever shepherd registered for sysid.Write — this syscall only
// changes which ring index on that shepherd receives stdio-class
// traffic. Splitting stdio onto its own ring keeps stdio backpressure
// (chatty fmt.Printf) from starving the blocking-delegate ring (or
// vice versa) when both share the same handler.
//
// arg0 = ring index (0..MaxRingsPerShepherd-1)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterStdioWriteRing(arg0, _, _, _, _, _ uint64) int64 {
	if arg0 >= uint64(ipc.MaxRingsPerShepherd) {
		return -22 // EINVAL
	}
	atomic.StoreInt32(&stdioWriteRingIdx, int32(arg0))
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
	isPageFaultReply := false
	if int(callerTID) < MaxDelegateThreads {
		info := &delegateCallInfos[callerTID]
		if info.InUse {
			isPageFaultReply = info.SysID == sysid.MmapPageFill
			// Verify the replying shepherd is the registered handler for this
			// delegation. Without this check, any shepherd that guesses a
			// caller's TID could forge a reply with an arbitrary return value.
			if info.HandlerSID != int16(replyingShepherd.PID) {
				return -1 // EPERM
			}
			// For MmapPageFill: map into faulting shepherd's page table.
			// Keep the page mapped in the handler too — this provides
			// mmap page cache coherence (same physical page visible to
			// both sides, so read/pread/write/pwrite through the handler
			// see the same data as mmap'd memory in the caller).
			if info.SysID == sysid.MmapPageFill && info.DataPagePA != 0 {
				// Check if faulting shepherd is still alive
				callerShepherd := proc.FindShepherdBySID(proc.ShepherdId(info.CallerSID))
				if callerShepherd == nil {
					// Shepherd died while waiting — unmap from handler, free the page
					hs := proc.FindShepherdBySID(proc.ShepherdId(info.HandlerSID))
					if hs != nil {
						kmem.UnmapUserPageWithL0(uintptr(info.DataPageVA), hs.PageTableL0PA)
						hs.Spans.Remove(info.DataPageVA, 4096)
					}
					kmem.ReleasePageByPA(info.DataPagePA)
				} else {
					elfFlags := uint32(kmem.ELF_PF_R)
					if info.WritableMapping {
						elfFlags |= kmem.ELF_PF_W
					}
					mapOK := kmem.MapPageInProcess(info.CallerSID, uintptr(info.CallerBufVA), info.DataPagePA, elfFlags)

					if !mapOK {
						klog.Errf("[mmap-verify] MAP FAIL PA=%x cVA=%x\n", uint64(info.DataPagePA), uint64(info.CallerBufVA))
					} else {
						// Mark as file-backed so CleanupShepherdPages Phase 1
						// skips this page. The handler (linux shepherd) still
						// holds a live PTE; handleFlushReply releases the page
						// after the handler has finished reading/writing.
						if desc := kmem.GetPageDescriptor(info.DataPagePA); desc != nil {
							desc.Flags |= kmem.PD_FILE_BACKED
						}
					}

					// Handler-side pageCache tracks the dual mapping.
					// On munmap/death, flushAndCleanupPages sends IPC
					// rounds to flush dirty pages and return handler VAs
					// for unmapping — no kernel-side tracking needed.
				}

				// Prevent reclaimDataPage from freeing it
				info.DataPagePA = 0
				info.InUse = false
			}

			// For MmapPageFlush: process response page (unmap handler VAs).
			// If count == 511, send another round and keep caller blocked.
			if info.SysID == sysid.MmapPageFlush {
				if handleFlushReply(callerTID, info) {
					// Another round sent — caller stays blocked, don't wake
					return 0
				}
				info.InUse = false
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
					klog.Errf("[DLG] unable to write to client buffer @0x%x, only %d of %d were written before a fault\n",
						uint64(info.CallerBufVA), actual, bytesToCopy)
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

			callerDead := info.CallerDead
			info.InUse = false
			info.CallerDead = false

			// If the caller died while this delegate was in flight (see
			// CleanupDelegateForDeadShepherd Part 1), there is no thread to
			// wake — the data-page cleanup above is the only cleanup needed.
			if callerDead {
				return 0
			}
		}
	}

	// Wake the blocked caller thread.
	// For MmapPageFill: the thread was blocked by a page fault, not a syscall.
	// Its saved registers (including RAX) must be preserved exactly so the CPU
	// can retry the faulting instruction after IRETQ. SetReturnValue (which
	// overwrites RAX) would corrupt the register state and lose the write.
	if isPageFaultReply {
		wakeDelegateCallerThreadNoReturn(callerSID, int32(callerTID))
	} else {
		wakeDelegateCallerThread(callerSID, int32(callerTID), returnVal)
	}

	return 0
}

// isCopyBackSyscall returns true if the given syscall ID uses the Read pattern:
// handler fills a data page and the kernel copies the result back to the caller.
// prefaultOutputBuffer ensures all pages spanned by the caller's output buffer
// are demand-mapped. Called while still in the caller's syscall context so
// HandleUserPageFault can use TTBR0 and CurrentShepherd() correctly.
func prefaultOutputBuffer(bufVA uintptr, bufLen uintptr) {
	const pageSize = 4096
	endVA := bufVA + bufLen
	for va := bufVA &^ (pageSize - 1); va < endVA; va += pageSize {
		pa := kmem.WalkUserPageTable(va)
		if pa == 0 {
			kmem.HandleUserPageFault(va, 0)
		}
	}
}

func isCopyBackSyscall(id sysid.ID) bool {
	switch id {
	case sysid.Read, sysid.Pread64, sysid.Fstat, sysid.Getdents64, sysid.Getcwd,
		sysid.Fstatfs, sysid.Statfs, sysid.Fstatat, sysid.Readlinkat, sysid.Readv:
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

// writeU16ToUser writes a uint16 to userspace memory via scratch mapping.
// Returns false if the page is not mapped (no demand-faulting — would use
// the wrong address space in cross-process contexts).
func writeU16ToUser(addr uintptr, val uint16, l0PA uintptr) bool {
	pa := kmem.WalkUserPageTableWithL0(addr, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := kmem.MapPAToKernelScratch(pa)
	if kernelVA == 0 {
		return false
	}
	*(*uint16)(unsafe.Pointer(kernelVA)) = val
	kmem.CleanCacheLine(kernelVA)
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
	kmem.CleanCacheLine(kernelVA)
	return true
}

// Linkname bridge functions — implemented in kmazarin/kmazarin/ipc_bridge.go
//
//go:linkname blockForDelegatedSyscall main.BlockForDelegatedSyscall
func blockForDelegatedSyscall() uintptr

//go:linkname wakeDelegateCallerThread main.WakeDelegateCallerThread
func wakeDelegateCallerThread(pid int16, tid int32, returnVal int64)

//go:linkname wakeDelegateCallerThreadNoReturn main.WakeDelegateCallerThreadNoReturn
func wakeDelegateCallerThreadNoReturn(pid int16, tid int32)

// RecordDelegateBlock stamps the current thread's DelegateBlockSinceTick +
// DelegateBlockSysID. Implemented in main; we call it just before
// blockForDelegatedSyscall so printEpochStatus can identify stuck delegated
// syscalls by (sid, sysid, blocked-for).
//
//go:linkname RecordDelegateBlock main.RecordDelegateBlock
func RecordDelegateBlock(sysID uint16)

//go:linkname readThreadRAX main.ReadThreadRAX
func readThreadRAX(pid int16, tid int32) uint64

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

	// Part 1: Dying shepherd was a CALLER — defer data-page reclaim to the
	// handler's Reply path. We cannot reclaim here because the handler's
	// uring msg for this delegate may still be queued; if we unmap the
	// page now, the handler will pop the msg later and fault on the
	// dangling r.dataVA. Marking CallerDead=true keeps InUse intact so
	// SyscallReply still walks the cleanup code (reclaim the data page,
	// clear InUse) but skips wakeDelegateCallerThread (caller is gone).
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if !info.InUse || info.CallerSID != pid {
			continue
		}
		info.CallerDead = true
	}

	// Part 2: Dying shepherd was a HANDLER — unregister all SysIDs.
	for sid := sysid.ID(0); sid < sysid.NumIDs; sid++ {
		if int16(atomic.LoadInt32(&syscallDelegates[sid].pid)) == pid {
			atomic.StoreInt32(&syscallDelegates[sid].pid, -1)
		}
	}

	// Part 3: Dying shepherd was a HANDLER — find orphaned callers.
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

// HasInFlightDelegateCallsAsCaller returns true if any delegateCallInfos entry
// is in use with CallerSID == pid. Used by TerminateShepherd to decide whether
// to defer page cleanup (two-phase death protocol).
func HasInFlightDelegateCallsAsCaller(pid int16) bool {
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if info.InUse && info.CallerSID == pid {
			return true
		}
	}
	return false
}

// CleanupDelegateForDeadShepherdHandlerOnly runs Parts 2+3 of delegate cleanup
// (dying shepherd as HANDLER) but skips Part 1 (dying shepherd as CALLER).
// This is used in the two-phase death protocol: the linux shepherd still needs
// the caller's data pages to complete in-flight reads/writes. Those pages are
// reclaimed later when SyscallReply runs normally, or as a safety net in
// completeDeferredCleanup.
func CleanupDelegateForDeadShepherdHandlerOnly(pid int16) int {
	orphanCount := 0

	// Part 2: Dying shepherd was a HANDLER — unregister all SysIDs.
	for sid := sysid.ID(0); sid < sysid.NumIDs; sid++ {
		if int16(atomic.LoadInt32(&syscallDelegates[sid].pid)) == pid {
			atomic.StoreInt32(&syscallDelegates[sid].pid, -1)
		}
	}

	// Part 3: Dying shepherd was a HANDLER — find orphaned callers.
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if !info.InUse || info.HandlerSID != pid {
			continue
		}
		info.DataPagePA = 0
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

// CleanupRemainingDelegateCallsForCaller is a safety net called from
// completeDeferredCleanup. It reclaims any delegateCallInfos entries where
// CallerSID == pid that were not already cleared by normal SyscallReply.
func CleanupRemainingDelegateCallsForCaller(pid int16) {
	for i := 0; i < MaxDelegateThreads; i++ {
		info := &delegateCallInfos[i]
		if !info.InUse || info.CallerSID != pid {
			continue
		}
		if info.DataPagePA != 0 {
			handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(info.HandlerSID))
			reclaimDataPage(info.DataPagePA, info.DataPageVA, info.HandlerSID, handlerShepherd)
		}
		info.InUse = false
	}
}

// SyscallDeathAck is the kernel handler for SysDeathAck. The linux shepherd
// calls this after all in-flight I/O for the dead shepherd has drained.
// arg0 = dead shepherd's SID.
//
//go:noinline
func SyscallDeathAck(arg0, _, _, _, _, _ uint64) int64 {
	deadSID := int16(arg0)
	completeDeferredCleanup(deadSID)
	return 0
}

//go:linkname completeDeferredCleanup main.CompleteDeferredCleanup
func completeDeferredCleanup(deadSID int16)

