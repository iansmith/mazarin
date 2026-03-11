package main

import "unsafe"

// x86_64 exception frame offsets (must match exceptions_amd64.s)
// Frame is pushed by CPU (RIP, CS, RFLAGS, RSP, SS) + our register saves.
const (
	excFrameRIP    = 128 // Interrupted RIP (PC)
	excFrameRSP    = 152 // Interrupted RSP
	excFrameRFLAGS = 144 // RFLAGS
)

// checkKernelGoroutinePreemptInternal checks if the Go runtime wants to
// preempt the current kernel goroutine and injects asyncPreempt into the
// exception frame if safe.
//
// Called from the timer IRQ handler via CheckKernelGoroutinePreempt ABI stub
// when the interrupted code is kernel code (CS=0x08) with svcDepth==0.
//
// Returns 1 if asyncPreempt was injected, 0 otherwise.
func checkKernelGoroutinePreemptInternal(framePtr uint64) uint64 {
	frame := (*[21]uint64)(unsafe.Pointer(uintptr(framePtr)))

	pc := uintptr(frame[excFrameRIP/8])
	sp := uintptr(frame[excFrameRSP/8])
	// x86_64 has no link register; pass 0 for lr.
	// isAsyncSafePoint doesn't use lr on amd64.
	var lr uintptr

	shouldPreempt, targetPC, resumePC := tryKernelAsyncPreempt(pc, sp, lr)
	if !shouldPreempt {
		return 0
	}

	// pushCall equivalent for x86_64 (mirrors runtime signal_amd64.go pushCall):
	// 1. Decrement RSP by 8 (push return address)
	//    Note: runtime uses goarch.PtrSize=8, and the SP was already 16-aligned
	//    from the interrupted code. Subtracting 8 makes it 8-aligned.
	//    asyncPreempt's SUBQ in its prologue will re-align to 16.
	// 2. Write resumePC at new RSP (simulates a CALL instruction)
	// 3. Set RIP = targetPC (asyncPreempt)
	newSP := sp - 8
	*(*uint64)(unsafe.Pointer(newSP)) = uint64(resumePC)

	frame[excFrameRSP/8] = uint64(newSP)
	frame[excFrameRIP/8] = uint64(targetPC)

	return 1
}
