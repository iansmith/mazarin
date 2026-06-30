//go:build arm64

package main

import (
	"reflect"
	"unsafe"

	"mazzy/kmazarin/klog"
)

// ctxTestThread is scratch state used ONLY by runContextMarshalSelfTest.
var ctxTestThread Thread

const ctxFrameSentinel = 0x5A5A5A5A_00000010 // base for synthetic exception-frame slots

// runContextMarshalSelfTest is the arm64 counterpart of the amd64 context-marshal
// boot selftest (MAZ-135 parity). It is NON-INVASIVE: it builds a synthetic
// exception frame with a distinct nonzero sentinel per slot, swaps the current
// thread to a scratch, and calls the REAL SaveContextFromFrame, then asserts every
// ThreadContext field was populated — the value-flow complement to the build-time
// cmd/frameaudit gate. arm64 has a single g home (X28), so there is no dual-home
// hazard to check. Runs IRQ-masked at a quiescent boot point (only thread 0),
// restoring the current thread before the reflection assertions (which allocate).
// Gated by kernel.toml ctx_marshal_test.
func runContextMarshalSelfTest() {
	// Synthetic ARM64 exception frame: x0-x30 at [0:30], ELR at [32], SPSR at
	// [33], SP at [36] (see SaveContextFromFrame). Every slot distinct + nonzero.
	var frame [40]uint64
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}

	daif := SaveAndDisableIRQs()
	old := GetCurrentThread()
	ctxTestThread.Context = ThreadContext{} // zero ⇒ "unwritten stays zero"
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(uintptr(unsafe.Pointer(&frame[0])))
	SetCurrentThreadGlobal(old)
	ctx := ctxTestThread.Context
	RestoreIRQs(daif)

	// Spot-check the frame→field mapping for the special regs and the g register.
	if ctx.X[28] != frame[28] || ctx.ELR != frame[32] || ctx.SPSR != frame[33] || ctx.SP != frame[36] {
		klog.Errf("[ctx-selftest] FAIL: arm64 frame mapping wrong (X28/ELR/SPSR/SP)\n")
		panic("runContextMarshalSelfTest: arm64 exception-frame slot mapping incorrect")
	}

	// Completeness: every field nonzero ⇒ the save wrote it (every source is nonzero).
	cv := reflect.ValueOf(ctx)
	ct := cv.Type()
	for i := range ct.NumField() {
		f := cv.Field(i)
		name := ct.Field(i).Name
		var zero bool
		switch f.Kind() {
		case reflect.Uint64:
			zero = f.Uint() == 0
		case reflect.Array: // X [31]uint64
			zero = true
			for j := range f.Len() {
				if f.Index(j).Uint() != 0 {
					zero = false
					break
				}
			}
		default:
			klog.Errf("[ctx-selftest] FAIL: ThreadContext.%s has unhandled kind %s\n", name, f.Kind())
			panic("runContextMarshalSelfTest: unhandled ThreadContext field kind")
		}
		if zero {
			klog.Errf("[ctx-selftest] FAIL: SaveContextFromFrame left ThreadContext.%s unwritten (zero)\n", name)
			panic("runContextMarshalSelfTest: a context field was not populated by the save")
		}
	}

	klog.Criticalf("[CTX]", "[ctx-selftest] OK — arm64 SaveContextFromFrame populates all %d ThreadContext fields\n", ct.NumField())

	// MAZ-147: deterministic RED→GREEN for the m.locks checkpoint (arch-neutral
	// save/re-arm contract + non-g0 gate). The arm64 asm restore (mlockRearmFromFrame)
	// is covered by objdump + the icount TCG sweep.
	runMLockCheckpointSelfTest()
}
