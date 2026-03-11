package main

import "unsafe"

// RISC-V exception frame offsets (must match exceptions_riscv64.s)
// Frame layout: x1(ra)=0, x2(sp)=8, x3(gp)=16, ..., x31(t6)=240, sepc=248, sstatus=256
const (
	excFrameRA   = 0   // X1 / RA (return address / link register)
	excFrameSP   = 8   // X2 / SP (interrupted stack pointer)
	excFrameSEPC = 248 // SEPC (interrupted PC)
)

// checkKernelGoroutinePreemptInternal checks if the Go runtime wants to
// preempt the current kernel goroutine and injects asyncPreempt into the
// exception frame if safe.
//
// Called from the timer IRQ handler via CheckKernelGoroutinePreempt ABI stub
// when the interrupted code is S-mode code with svcDepth==0.
//
// Returns 1 if asyncPreempt was injected, 0 otherwise.
func checkKernelGoroutinePreemptInternal(framePtr uint64) uint64 {
	frame := (*[33]uint64)(unsafe.Pointer(uintptr(framePtr)))

	pc := uintptr(frame[excFrameSEPC/8])
	sp := uintptr(frame[excFrameSP/8])
	lr := uintptr(frame[excFrameRA/8])

	shouldPreempt, targetPC, resumePC := tryKernelAsyncPreempt(pc, sp, lr)
	if !shouldPreempt {
		return 0
	}

	// pushCall equivalent for RISC-V (mirrors runtime signal_riscv64.go pushCall):
	// 1. Decrement SP by 8 (PtrSize)
	// 2. Save old RA at new SP
	// 3. Set RA = resumePC
	// 4. Set PC (SEPC) = targetPC (asyncPreempt)
	oldRA := frame[excFrameRA/8]
	newSP := sp - 8

	// Write old RA to the goroutine's stack
	*(*uint64)(unsafe.Pointer(newSP)) = oldRA

	// Update exception frame
	frame[excFrameSP/8] = uint64(newSP)
	frame[excFrameRA/8] = uint64(resumePC)
	frame[excFrameSEPC/8] = uint64(targetPC)

	return 1
}
