//go:build amd64

package main

import (
	"unsafe"
)

// xmmSaveArea is defined in exceptions_amd64.s — global buffer where
// common_exception_entry saves XMM0-XMM15 on exception entry.
var xmmSaveArea [256]byte

// interruptedRIP is the x86 software equivalent of ARM64's ELR_EL1 register.
// common_exception_entry stashes the interrupted RIP (exception frame offset
// 128) here on EVERY exception entry, so readELR_EL1() can return it from any
// handler. Like ELR_EL1 it holds the most-recent exception's PC and is valid
// only until the next exception clobbers it. Global (single-CPU, matching
// xmmSaveArea above); per-CPU-ize alongside xmmSaveArea when x86 goes SMP.
var interruptedRIP uint64

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
		// MAZ-135: kernel TLS-g ([kmazarinFSBase-8]) was overwritten by
		// common_exception_entry with the handler's g, so the interrupted kernel
		// thread's TLS-g is unrecoverable here. Fall back to R14 (kernel threads
		// are not the morestack victims).
		t.Context.TLSG = frame[13]
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
	// Copy XMM state from global save area to per-thread context.
	// common_exception_entry saved XMM0-15 to xmmSaveArea before any Go code
	// could clobber them. We must copy to ThreadContext so that when this thread
	// is rescheduled, load_context_and_iretq restores the correct XMM state.
	copy(t.Context.XMM[:], xmmSaveArea[:])
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

// SaveCurrentThreadContext saves the current thread's context from individual x86_64 registers.
// Called from assembly with all 15 GPRs plus RIP, RFLAGS, and RSP.
//
// frameaudit:save — must populate every ThreadContext field.
//
//go:nosplit
func SaveCurrentThreadContext(
	rax, rbx, rcx, rdx, rsi, rdi, rbp uint64,
	r8, r9, r10, r11, r12, r13, r14, r15 uint64,
	rip, rflags, rsp uint64,
) {
	t := GetCurrentThread()
	if t == nil {
		return
	}
	t.Context.RAX = rax
	t.Context.RBX = rbx
	t.Context.RCX = rcx
	t.Context.RDX = rdx
	t.Context.RSI = rsi
	t.Context.RDI = rdi
	t.Context.RBP = rbp
	t.Context.R8 = r8
	t.Context.R9 = r9
	t.Context.R10 = r10
	t.Context.R11 = r11
	t.Context.R12 = r12
	t.Context.R13 = r13
	t.Context.R14 = r14
	t.Context.R15 = r15
	t.Context.RIP = rip
	t.Context.RFLAGS = rflags
	t.Context.RSP = rsp
	t.Context.FSBase = savedExcFSBase // Use saved value (common_exception_entry saves before WRMSR to kernel)
	// SaveCurrentThreadContext is only called from kernel context (INT $0x80 path),
	// so hardcode kernel segment selectors.
	t.Context.CS = kernelCS
	t.Context.SS = kernelSS
	// MAZ-135: TLSG is the second g home ([FS_BASE-8]). This is a kernel-context
	// save, so mirror SaveContextFromFrame's kernel-CS branch: the kernel TLS-g
	// may have been clobbered by common_exception_entry, and kernel threads are
	// not the morestack victims, so use R14 as the dual-home g.
	t.Context.TLSG = r14
	// Copy XMM state from global save area (same as SaveContextFromFrame).
	copy(t.Context.XMM[:], xmmSaveArea[:])
}
