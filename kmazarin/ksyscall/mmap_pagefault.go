package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

func init() {
	kmem.OnFileMappedPageFault = handleFileMappedPageFault
}

// handleFileMappedPageFault handles a page fault in a file-backed mmap region.
// It allocates a physical frame, sends a read request to the linux shepherd
// to fill it with file data, and blocks the faulting thread. The assembly
// exception handler checks GetSyscallSwitchTarget after we return and performs
// the context switch.
//
//go:noinline
func handleFileMappedPageFault(faultAddr uintptr, fm *proc.FileMapping) bool {
	pageAddr := faultAddr &^ (kmem.PageSize - 1)

	// Find the linux shepherd (delegate handler for Read)
	handlerSID := int16(atomic.LoadInt32(&syscallDelegates[sysid.Read].pid))
	if handlerSID < 0 {
		klog.Errf("[mmap-pf] no Read delegate handler\n")
		return false
	}
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
	if handlerShepherd == nil {
		klog.Errf("[mmap-pf] handler shepherd gone\n")
		return false
	}

	// Allocate a physical frame for the page
	callerSID := int16(proc.CurrentShepherd().PID)
	framePA := kmem.AllocPage(kmem.PageFileMmap, callerSID)
	if framePA == 0 {
		klog.Errf("[mmap-pf] ENOMEM\n")
		return false
	}

	// Zero the frame
	scratchVA := kmem.MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(framePA)
		return false
	}
	zeroPage(scratchVA)

	// Serial diagnostic: confirm allocation and zeroing
	kmem.SerialPuts("[mmap-pf] alloc PA=")
	kmem.SerialHex16(uint64(framePA))
	kmem.SerialPuts(" faultVA=")
	kmem.SerialHex16(uint64(pageAddr))
	// Verify zero through scratch mapping
	word0 := *(*uint64)(unsafe.Pointer(scratchVA))
	kmem.SerialPuts(" z0=")
	kmem.SerialHex16(word0)
	kmem.SerialPuts("\r\n")

	// Map the frame into the linux shepherd's address space so it can fill it
	handlerDataVA := bumpAllocForShepherd(handlerShepherd, 4096)
	if handlerDataVA == 0 {
		kmem.ReleasePageByPA(framePA)
		return false
	}
	kmem.MapPageInProcess(handlerSID, uintptr(handlerDataVA), framePA, 0) // RW
	handlerShepherd.Spans.Add(handlerDataVA, 4096)

	// Compute file offset for this page
	pageOffset := uint64(pageAddr) - fm.StartVA
	fileOffset := fm.FileOffset + pageOffset

	// TEMPORARILY DISABLED to isolate mmap corruption bug:
	// logMmapPageFault(callerSID, uint64(pageAddr), uint64(framePA), int32(fm.FD), fileOffset, handlerDataVA)

	// Get current thread info for delegation tracking
	_, callerTID := getCurrentThreadSIDAndTID()

	// Set up DelegateCallInfo for the reply path
	if int(callerTID) >= MaxDelegateThreads {
		reclaimDataPage(framePA, handlerDataVA, handlerSID, handlerShepherd)
		return false
	}
	info := &delegateCallInfos[callerTID]
	info.DataPagePA = framePA
	info.DataPageVA = handlerDataVA
	info.HandlerSID = handlerSID
	info.CallerSID = callerSID
	info.CallerBufVA = uintptr(pageAddr) // page-aligned fault VA (mapping target)
	info.CallerBufLen = 4096
	info.CallerL0PA = uintptr(kmem.ReadCurrentL0PA())
	info.SysID = sysid.MmapPageFill
	info.WritableMapping = fm.Writable
	info.InUse = true

	// Send MmapPageFill request to linux shepherd
	reqPayload := ipc.FSDelegateReqPayload{
		SysID:     uint16(sysid.MmapPageFill),
		CallerSID: fm.CallerSID,
		CallerTID: callerTID,
		Args:      [6]uint64{uint64(fm.FD), fileOffset, 4096, 0, 0, 0},
		DataVA:    handlerDataVA,
		DataLen:   4096,
	}
	msg := ipc.EncodeFSDelegateReq(&reqPayload)
	result, _ := uringSendKernel(-1, handlerSID, uintptr(unsafe.Pointer(&msg)))
	if result < 0 {
		info.InUse = false
		reclaimDataPage(framePA, handlerDataVA, handlerSID, handlerShepherd)
		klog.Errf("[mmap-pf] uring send failed\n")
		return false
	}

	// Block the faulting thread until the linux shepherd fills the page
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		klog.Errf("[mmap-pf] NO NEXT THREAD\n")
		return false
	}
	SetSyscallSwitchTarget(ctx)

	return true
}

// logMmapPageFault logs page fault details. Separated from handleFileMappedPageFault
// so the nosplit chain doesn't grow. Uses klog which requires a normal Go stack.
//
//go:noinline
func logMmapPageFault(sid int16, faultVA, pa uint64, fd int32, fileOff, handlerVA uint64) {
	klog.Logf("[mmap-pf] sid=%d VB=%x PA=%x fd=%d off=%x hVB=%x\n",
		sid, faultVA, pa, fd, fileOff, handlerVA)
}

// logMmapReply logs the reply-side mapping details.
//
//go:noinline
func logMmapReply(callerSID int16, callerVA uintptr, pa uintptr, handlerVA uint64, flags uint32) {
	klog.Logf("[mmap-reply] sid=%d cVA=%x PA=%x hVA=%x fl=%x\n",
		callerSID, callerVA, pa, handlerVA, flags)
}

// diagMmapWake prints diagnostic info just before waking a page-fault-blocked thread.
// Reads the first 8 bytes of the mmap page through the caller's page table
// to verify the page is still clean before the thread resumes.
//
//go:noinline
func diagMmapWake(callerSID int16, callerTID int32, callerVA uintptr, callerL0PA uintptr) {
	if callerVA == 0 || callerL0PA == 0 {
		return
	}
	// Read first 8 bytes through caller's page table
	word0, ok := kmem.ReadUserUint64WithL0(callerVA, callerL0PA)
	kmem.SerialPuts("[wake-diag] sid=")
	kmem.SerialHex16(uint64(callerSID))
	kmem.SerialPuts(" tid=")
	kmem.SerialHex16(uint64(callerTID))
	kmem.SerialPuts(" VA=")
	kmem.SerialHex16(uint64(callerVA))
	kmem.SerialPuts(" w0=")
	kmem.SerialHex16(word0)
	if !ok {
		kmem.SerialPuts("(FAIL)")
	}
	// Also read saved RAX from thread context via bridge
	rax := readThreadRAX(callerSID, callerTID)
	kmem.SerialPuts(" RAX=")
	kmem.SerialHex16(rax)
	kmem.SerialPuts("\r\n")
}
