//go:build arm64

package main

import (
	"unsafe"
	_ "unsafe" // for go:linkname
)

// g is a partial mirror of runtime.g for accessing field offsets.
// We only need the fields we're going to set from the timer interrupt.
// This MUST match the actual runtime.g layout for Go 1.25.5.
type runtimeG struct {
	stack struct {
		lo uintptr
		hi uintptr
	}
	stackguard0 uintptr
	stackguard1 uintptr
	_panic      uintptr
	_defer      uintptr
	m           uintptr
	sched       struct {
		sp   uintptr
		pc   uintptr
		g    uintptr
		ctxt uintptr
		ret  uintptr
		lr   uintptr
		bp   uintptr
	}
	syscallsp uintptr
	syscallpc uintptr
	stktopsp  uintptr
	param     uintptr
	atomicstatus uint32
	stackLock    uint32
	goid         uint64
	schedlink    uintptr
	waitsince    int64
	waitreason   uint8
	preempt      bool // This is the field we set for cooperative preemption
	// ... many more fields, but we only need offset up to preempt
}

// stackPreempt is the poison value for g.stackguard0 that triggers preemption.
// This is defined as 0xfffffade in the Go runtime.
const stackPreempt = 0xfffffffffffffade

// GetPreemptOffsets returns offsets into the g struct for cooperative preemption.
// This is called via go:linkname from kirq.InitPreemption().
//
func GetPreemptOffsets() struct {
	StackGuard0Offset uintptr
	PreemptOffset     uintptr
	GStatusOffset     uintptr
	StackLoOffset     uintptr
	StackHiOffset     uintptr
	StackPreemptValue uintptr
	GRunning          uint32
	GScan             uint32
	GMOffset          uintptr // Offset of g.m from g pointer
	MG0Offset         uintptr // Offset of m.g0 from m pointer (always 0)
} {
	var g runtimeG
	return struct {
		StackGuard0Offset uintptr
		PreemptOffset     uintptr
		GStatusOffset     uintptr
		StackLoOffset     uintptr
		StackHiOffset     uintptr
		StackPreemptValue uintptr
		GRunning          uint32
		GScan             uint32
		GMOffset          uintptr
		MG0Offset         uintptr
	}{
		StackGuard0Offset: unsafe.Offsetof(g.stackguard0),
		PreemptOffset:     unsafe.Offsetof(g.preempt),
		GStatusOffset:     unsafe.Offsetof(g.atomicstatus),
		StackLoOffset:     unsafe.Offsetof(g.stack.lo),
		StackHiOffset:     unsafe.Offsetof(g.stack.hi),
		StackPreemptValue: stackPreempt,
		GRunning:          2,      // _Grunning constant
		GScan:             0x1000, // _Gscan mask
		GMOffset:          unsafe.Offsetof(g.m),
		MG0Offset:         0, // m.g0 is first field in m struct
	}
}
