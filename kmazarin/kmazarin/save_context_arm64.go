//go:build arm64

package main

import (
	"mazzy/kmazarin/serial"
	"unsafe"
)

// Context switch diagnostics — written by doContextSwitchABI0 when ELR=0 detected
var ctxSwitchDiagTID int32
var ctxSwitchDiagSP uint64
var ctxSwitchDiagLR uint64
var ctxSwitchDiagSPSR uint64

// SaveContextFromFrame saves the current thread's context from an ARM64 exception frame.
// Frame layout: x0-x27 at [0:27], x28-x30 at [28:30], ELR at [32], SPSR at [33], SP at [36].
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	t := GetCurrentThread()
	if t == nil {
		return
	}

	// Handle nil frame (can happen in tests)
	if framePtr == 0 {
		return
	}
	frame := (*[40]uint64)(unsafe.Pointer(framePtr))

	for i := 0; i < 28; i++ {
		t.Context.X[i] = frame[i]
	}

	t.Context.X[28] = frame[28]
	t.Context.X[29] = frame[29]
	t.Context.X[30] = frame[30]
	t.Context.SP = frame[36]
	t.Context.ELR = frame[32]
	t.Context.SPSR = frame[33]
}

// doContextSwitchABI0 is the ABI0 entry point for context switching.
// Called from assembly via DoContextSwitch stub with 2 arguments.
// targetPtr is actually a pointer to ThreadContext (returned by getSyscallSwitchTargetInternal).
//
//go:nosplit
//go:noinline
func doContextSwitchABI0(framePtr uint64, targetPtr uint64) uint64 {
	// Find which thread owns this context
	targetCtx := (*ThreadContext)(unsafe.Pointer(uintptr(targetPtr)))

	// Find the thread index by matching context pointer
	// Use StaticList.Data slice which points to threadListData
	targetIdx := int32(-1)
	for i := int32(0); i < MaxThreads; i++ {
		if &threadList.Data[i].Context == targetCtx {
			targetIdx = i
			break
		}
	}

	if targetIdx < 0 {
		serial.PollWrite('!')
		return 0
	}

	// Use NormalSchedulerFunc for production calls from assembly
	ctx := doContextSwitchImpl(&NormalSchedulerFunc, uintptr(framePtr), targetIdx)
	if ctx != nil && ctx.ELR == 0 {
		// ELR=0 detected — store diagnostic info in globals for the crash handler
		serial.PollWrite('Z')
		ctxSwitchDiagTID = int32(targetIdx)
		ctxSwitchDiagSP = ctx.SP
		ctxSwitchDiagLR = ctx.X[30]
		ctxSwitchDiagSPSR = ctx.SPSR
	}
	return uint64(uintptr(unsafe.Pointer(ctx)))
}

// SaveCurrentThreadContext saves the current thread's context from individual ARM64 registers.
// Called from assembly with all 31 GPRs plus SP, ELR, and SPSR.
//
//go:nosplit
func SaveCurrentThreadContext(
	x0, x1, x2, x3, x4, x5, x6, x7 uint64,
	x8, x9, x10, x11, x12, x13, x14, x15 uint64,
	x16, x17, x18, x19, x20, x21, x22, x23 uint64,
	x24, x25, x26, x27, x28, x29, x30 uint64,
	sp, elr, spsr uint64,
) {
	t := GetCurrentThread()
	if t == nil {
		return
	}
	t.Context.X[0] = x0
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
	t.Context.SP = sp
	t.Context.ELR = elr
	t.Context.SPSR = spsr
}
