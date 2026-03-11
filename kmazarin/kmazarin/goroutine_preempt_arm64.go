package main

import "unsafe"

// ARM64 exception frame offsets (must match exceptions_arm64.s)
const (
	excFrameELR   = 256 // ELR_EL1 (interrupted PC)
	excFrameSPSR  = 264 // SPSR_EL1
	excFrameSPEL0 = 288 // SP_EL0 (interrupted SP)
	excFrameX29   = 232 // X29 (frame pointer)
	excFrameX30   = 240 // X30 (link register)
)

// checkKernelGoroutinePreemptInternal checks if the Go runtime wants to
// preempt the current kernel goroutine and injects asyncPreempt into the
// exception frame if safe.
//
// Called from the timer IRQ handler via CheckKernelGoroutinePreempt ABI stub
// when the interrupted code is kernel code at EL1t with svcDepth==0.
//
// Returns 1 if asyncPreempt was injected, 0 otherwise.
func checkKernelGoroutinePreemptInternal(framePtr uint64) uint64 {
	frame := (*[40]uint64)(unsafe.Pointer(uintptr(framePtr)))

	pc := uintptr(frame[excFrameELR/8])
	sp := uintptr(frame[excFrameSPEL0/8])
	lr := uintptr(frame[excFrameX30/8])

	shouldPreempt, targetPC, resumePC := tryKernelAsyncPreempt(pc, sp, lr)
	if !shouldPreempt {
		return 0
	}

	// pushCall equivalent for ARM64 (mirrors runtime signal_arm64.go pushCall):
	// 1. Decrement SP by 16 (16-byte alignment required)
	// 2. Save old LR at new SP
	// 3. Save old FP (X29) at new SP - 8 (for frame pointer chain)
	// 4. Set LR = resumePC
	// 5. Set PC (ELR) = targetPC (asyncPreempt)
	oldLR := frame[excFrameX30/8]
	oldFP := frame[excFrameX29/8]
	newSP := sp - 16

	// Write old LR and FP to the goroutine's stack.
	// FP is written below SP (at sp-8) matching runtime's pushCall exactly.
	// gentraceback knows about this extra slot for frame pointer unwinding.
	*(*uint64)(unsafe.Pointer(newSP)) = oldLR
	*(*uint64)(unsafe.Pointer(newSP - 8)) = oldFP

	// Update exception frame
	frame[excFrameSPEL0/8] = uint64(newSP)
	frame[excFrameX30/8] = uint64(resumePC)
	frame[excFrameELR/8] = uint64(targetPC)

	return 1
}
