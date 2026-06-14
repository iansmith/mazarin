//go:build amd64

package main

import (
	"unsafe"
)

// interruptedRIP is the x86 software equivalent of ARM64's ELR_EL1 register.
// common_exception_entry stashes the interrupted RIP (exception frame offset
// 128) here on EVERY exception entry, so readELR_EL1() can return it from any
// handler. Like ELR_EL1 it holds the most-recent exception's PC and is valid
// only until the next exception clobbers it. Global (single-CPU);
// per-CPU-ize when x86 goes SMP.
var interruptedRIP uint64

// savedExcKernelTLSG holds the interrupted kernel thread's live TLS-g
// ([kmazarinFSBase-8]) captured at the first instructions of
// common_exception_entry, before the handler overwrites that slot with the
// handler g0. The kernel-mode branch of SaveContextFromFrame uses it for a
// faithful dual-home g restore (in the systemstack exit window frame R14 is
// the stale g0 while the real curg lives only in this TLS slot). Global
// (single-CPU, like savedExcFSBase / interruptedRIP);
// per-CPU-ize for SMP. MAZ-135/MAZ-136.
var savedExcKernelTLSG uint64

// gLooksValid reports whether v is a plausible kernel goroutine pointer: nonzero
// and low-canonical (bits 63:47 clear). Every kernel g — g0 and heap-allocated
// g's alike — lives in the low half of the address space, so a high-half or
// non-canonical value means the captured TLS-g slot held garbage (early boot /
// clone-child bringup before TLS-g was established) and must not be trusted.
//
//go:nosplit
func gLooksValid(v uint64) bool {
	return v != 0 && v < 0x0000800000000000
}

// SaveContextFromFrame saves the current thread's context from an x86_64 exception frame.
//
// Frame layout (pushed by exception handler):
//
//	[0]=RAX [1]=RBX [2]=RCX [3]=RDX [4]=RSI [5]=RDI [6]=RBP
//	[7]=R8 [8]=R9 [9]=R10 [10]=R11 [11]=R12 [12]=R13 [13]=R14 [14]=R15
//	[15]=error_code [16]=RIP [17]=CS [18]=RFLAGS [19]=RSP [20]=SS
//
// frameaudit:save — must populate every ThreadContext field.
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	t := GetCurrentThread()
	if t == nil {
		return
	}
	if framePtr == 0 {
		return
	}
	frame := (*[21]uint64)(unsafe.Pointer(framePtr))

	t.Context.RAX = frame[0]
	t.Context.RBX = frame[1]
	t.Context.RCX = frame[2]
	t.Context.RDX = frame[3]
	t.Context.RSI = frame[4]
	t.Context.RDI = frame[5]
	t.Context.RBP = frame[6]
	t.Context.R8 = frame[7]
	t.Context.R9 = frame[8]
	t.Context.R10 = frame[9]
	t.Context.R11 = frame[10]
	t.Context.R12 = frame[11]
	t.Context.R13 = frame[12]
	t.Context.R14 = frame[13]
	t.Context.R15 = frame[14]
	// frame[15] = error code, not saved to ThreadContext
	t.Context.RIP = frame[16]
	t.Context.CS = frame[17]
	t.Context.RFLAGS = frame[18]
	t.Context.RSP = frame[19]
	t.Context.SS = frame[20]
	// For user-mode exceptions, savedExcFSBase holds the user's FS_BASE
	// (saved by common_exception_entry before switching to kernel TLS).
	// For kernel-mode exceptions (CS=0x08), common_exception_entry skips
	// saving FS_BASE to prevent nested exceptions from overwriting the user
	// value. But when preempting a kernel thread, we need the kernel FSBase.
	if frame[17] == kernelCS {
		t.Context.FSBase = kmazarinFSBase
		// MAZ-135/MAZ-136: the live kernel TLS-g was captured into
		// savedExcKernelTLSG at the first instructions of common_exception_entry,
		// before the handler overwrote [kmazarinFSBase-8] with its own g0. Prefer
		// it over frame R14: in the systemstack exit window R14 is the stale g0
		// while the real curg lives only in that TLS slot, and restoring the stale
		// value scrambles the runtime's g state (GC checkmark failures, netpoll
		// throws, corrupted resume RIPs — the MAZ-136 crash family).
		//
		// BUT the TLS slot is only meaningful once the interrupted kernel code has
		// established its TLS-g. During early boot and clone-child bringup it holds
		// garbage (a non-canonical value), and propagating that gives the resumed
		// thread a garbage g -> #GP the moment the runtime syncs TLS-g into R14.
		// Fall back to the always-valid frame R14 when the captured slot is not a
		// plausible kernel g pointer.
		if gLooksValid(savedExcKernelTLSG) {
			t.Context.TLSG = savedExcKernelTLSG
		} else {
			t.Context.TLSG = frame[13]
		}
	} else {
		t.Context.FSBase = savedExcFSBase
		// MAZ-135: capture the SECOND g home — the live user TLS-g at
		// [user-FS_BASE - 8]. Entry only switched FS_BASE + wrote the KERNEL TLS,
		// so the USER TLS slot still holds the interrupted thread's g (= curg even
		// when R14 is the stale g0 of systemstack's exit window). Restoring this
		// faithfully (instead of forcing TLS-g = R14) is the fix. Ring-0 read of
		// the user TLS page is safe (SMAP effectively off; the page is mapped — the
		// thread was using g via it).
		if savedExcFSBase != 0 {
			t.Context.TLSG = *(*uint64)(unsafe.Pointer(uintptr(savedExcFSBase - 8)))
		} else {
			t.Context.TLSG = frame[13]
		}
	}
	// Copy XMM state from THIS exception level's per-frame slot to per-thread
	// context. MAZ-139 item 2: common_exception_entry no longer saves XMM to the
	// single global xmmSaveArea (which an inner nested exception would clobber);
	// it now saves the interrupted XMM into a 256-byte slot reserved just below
	// the GPR frame, at framePtr-256. Read it from there so a thread suspended
	// mid-handler is rescheduled (via load_context_and_iretq) with its OWN XMM,
	// not whatever the innermost exception happened to leave behind.
	copy(t.Context.XMM[:], (*[256]byte)(unsafe.Pointer(framePtr-256))[:])
	// MAZ-136: no IST or RSP0 state is saved per context — TSS.IST1 AND
	// TSS.RSP0 are global nesting cursors; the abandoning handler's level
	// on each is retired by load_context_and_iretq itself (IST ROTATION and
	// RSP0 ROTATION banners, exceptions_amd64.s).
}

// doContextSwitchABI0 is the ABI0 entry point for context switching.
// Called from assembly via DoContextSwitch stub with 2 arguments.
// targetPtr is a pointer to ThreadContext (returned by getSyscallSwitchTargetInternal).
//
//go:nosplit
//go:noinline
func doContextSwitchABI0(framePtr uint64, targetPtr uint64) uint64 {
	targetCtx := (*ThreadContext)(unsafe.Pointer(uintptr(targetPtr)))

	targetIdx := int32(-1)
	for i := int32(0); i < MaxThreads; i++ {
		if &threadList.Data[i].Context == targetCtx {
			targetIdx = i
			break
		}
	}

	if targetIdx < 0 {
		return 0
	}

	ctx := doContextSwitchImpl(&NormalSchedulerFunc, uintptr(framePtr), targetIdx)
	return uint64(uintptr(unsafe.Pointer(ctx)))
}
