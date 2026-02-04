//go:build riscv64 && !test_stubs

package main

import (
	"sync/atomic"
	"unsafe"
)

// SaveContextFromFrame saves the current thread's context from a RISC-V trap frame.
//
// Frame layout (31 GPRs x1-x31 + sepc + sstatus):
//
//	Frame[0]=x1(ra), Frame[1]=x2(sp), ..., Frame[30]=x31(t6)
//	Frame[31]=sepc, Frame[32]=sstatus
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t == nil {
		return
	}
	if framePtr == 0 {
		return
	}
	frame := (*[33]uint64)(unsafe.Pointer(framePtr))

	// x1-x31 from frame[0..30] into X[1..31]
	for i := 0; i < 31; i++ {
		t.Context.X[i+1] = frame[i]
	}
	t.Context.SEPC = frame[31]
	t.Context.SSTATUS = frame[32]
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

// SaveCurrentThreadContext saves the current thread's context from individual RISC-V registers.
// Called from assembly with 31 GPRs (x1-x31) plus sepc and sstatus.
//
//go:nosplit
func SaveCurrentThreadContext(
	x1, x2, x3, x4, x5, x6, x7, x8 uint64,
	x9, x10, x11, x12, x13, x14, x15, x16 uint64,
	x17, x18, x19, x20, x21, x22, x23, x24 uint64,
	x25, x26, x27, x28, x29, x30, x31 uint64,
	sepc, sstatus uint64,
) {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t == nil {
		return
	}
	t.Context.X[1] = x1
	t.Context.X[2] = x2
	t.Context.X[3] = x3
	t.Context.X[4] = x4
	t.Context.X[5] = x5
	t.Context.X[6] = x6
	t.Context.X[7] = x7
	t.Context.X[8] = x8
	t.Context.X[9] = x9
	t.Context.X[10] = x10
	t.Context.X[11] = x11
	t.Context.X[12] = x12
	t.Context.X[13] = x13
	t.Context.X[14] = x14
	t.Context.X[15] = x15
	t.Context.X[16] = x16
	t.Context.X[17] = x17
	t.Context.X[18] = x18
	t.Context.X[19] = x19
	t.Context.X[20] = x20
	t.Context.X[21] = x21
	t.Context.X[22] = x22
	t.Context.X[23] = x23
	t.Context.X[24] = x24
	t.Context.X[25] = x25
	t.Context.X[26] = x26
	t.Context.X[27] = x27
	t.Context.X[28] = x28
	t.Context.X[29] = x29
	t.Context.X[30] = x30
	t.Context.X[31] = x31
	t.Context.SEPC = sepc
	t.Context.SSTATUS = sstatus
}
