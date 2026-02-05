//go:build amd64 && !test_stubs

package main

import (
	"unsafe"
)

// SaveContextFromFrame saves the current thread's context from an x86_64 exception frame.
//
// Frame layout (pushed by exception handler):
//
//	[0]=RAX [1]=RBX [2]=RCX [3]=RDX [4]=RSI [5]=RDI [6]=RBP
//	[7]=R8 [8]=R9 [9]=R10 [10]=R11 [11]=R12 [12]=R13 [13]=R14 [14]=R15
//	[15]=error_code [16]=RIP [17]=CS [18]=RFLAGS [19]=RSP [20]=SS
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
	// frame[17] = CS, not saved
	t.Context.RFLAGS = frame[18]
	t.Context.RSP = frame[19]
	// frame[20] = SS, not saved
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
}
