//go:build amd64

package main

import (
	"reflect"
	"unsafe"

	"mazzy/kmazarin/klog"
)

// Scratch state used ONLY by runContextMarshalSelfTest. ctxTestTLS[0] is the
// synthetic user TLS-g (savedExcFSBase-8 points at it during the test).
var (
	ctxTestThread Thread
	ctxTestTLS    [4]uint64
)

// runContextMarshalSelfTest is a NON-INVASIVE boot selftest (MAZ-135 guard). It
// does NOT change the savers. It builds synthetic inputs with a distinct nonzero
// sentinel per field, points the resolve-globals (savedExcFSBase TLS-g, xmmSaveArea)
// at sentinels, swaps the current thread to a scratch, and calls the REAL
// SaveContextFromFrame and SaveCurrentThreadContext. It then asserts every
// ThreadContext field is nonzero — i.e. the save actually wrote it, catching the
// MAZ-135 omission class (a field added to the struct but missed by a save site) —
// and that the dual-home TLSG is sourced correctly. frameaudit catches the same
// omission at build time by symbol presence; this catches it by value flow, and
// also catches a wrong-source wiring that presence cannot see.
//
// The global swaps run IRQ-masked at a quiescent boot point (only thread 0 exists);
// every global is restored before the reflection assertions (which allocate, so
// they run after IRQs are restored). Gated by kernel.toml ctx_marshal_test.
func runContextMarshalSelfTest() {
	frameCtx := saveFromSyntheticFrame()
	regsCtx := saveFromSyntheticRegs()

	assertAllFieldsWritten("SaveContextFromFrame", &frameCtx)
	assertAllFieldsWritten("SaveCurrentThreadContext", &regsCtx)

	klog.Criticalf("[CTX]", "[ctx-selftest] OK — both amd64 savers populate all %d ThreadContext fields; dual-home TLSG verified\n",
		reflect.TypeFor[ThreadContext]().NumField())
}

const (
	ctxFrameSentinel = 0x5A5A5A5A_00000010 // base for synthetic exception-frame slots
	ctxRegSentinel   = 0x6B6B6B6B_00000010 // base for synthetic SaveCurrentThreadContext regs
	ctxTLSSentinel   = 0x71170000_DEADBEEF // synthetic user TLS-g
)

// saveFromSyntheticFrame runs the REAL SaveContextFromFrame against a user-mode
// synthetic frame and returns the resulting ThreadContext. Verifies the dual-home
// TLSG is read from the user TLS slot (not R14).
func saveFromSyntheticFrame() ThreadContext {
	var frame [21]uint64
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[17] = userCS // CS → user branch (the dual-home TLS-read path)

	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	oldFSBase := savedExcFSBase
	var oldXMM [256]byte
	copy(oldXMM[:], xmmSaveArea[:])

	ctxTestTLS[0] = ctxTLSSentinel
	savedExcFSBase = uint64(uintptr(unsafe.Pointer(&ctxTestTLS[1]))) // base-8 == &ctxTestTLS[0]
	fillXMMSentinel()

	ctxTestThread.Context = ThreadContext{} // zero ⇒ "unwritten stays zero"
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(uintptr(unsafe.Pointer(&frame[0])))
	SetCurrentThreadGlobal(oldThread)

	savedExcFSBase = oldFSBase
	copy(xmmSaveArea[:], oldXMM[:])
	ctx := ctxTestThread.Context
	RestoreIRQs(daif)

	if ctx.TLSG != uint64(ctxTLSSentinel) {
		klog.Errf("[ctx-selftest] FAIL: SaveContextFromFrame TLSG=%#x, want user TLS sentinel %#x (dual-home not read from TLS)\n",
			ctx.TLSG, uint64(ctxTLSSentinel))
		panic("runContextMarshalSelfTest: TLSG not sourced from the user TLS slot")
	}
	if ctx.RIP != frame[16] || ctx.RSP != frame[19] || ctx.CS != userCS {
		klog.Errf("[ctx-selftest] FAIL: SaveContextFromFrame frame mapping wrong (RIP/RSP/CS)\n")
		panic("runContextMarshalSelfTest: exception-frame slot mapping incorrect")
	}
	return ctx
}

// saveFromSyntheticRegs runs the REAL SaveCurrentThreadContext against synthetic
// register values and returns the resulting ThreadContext.
func saveFromSyntheticRegs() ThreadContext {
	s := func(i int) uint64 { return ctxRegSentinel + uint64(i) }

	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	oldFSBase := savedExcFSBase
	var oldXMM [256]byte
	copy(oldXMM[:], xmmSaveArea[:])

	savedExcFSBase = ctxTLSSentinel // FSBase source; nonzero
	fillXMMSentinel()

	ctxTestThread.Context = ThreadContext{}
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveCurrentThreadContext(
		s(0), s(1), s(2), s(3), s(4), s(5), s(6),
		s(7), s(8), s(9), s(10), s(11), s(12), s(13), s(14),
		s(15), s(16), s(17),
	)
	SetCurrentThreadGlobal(oldThread)

	savedExcFSBase = oldFSBase
	copy(xmmSaveArea[:], oldXMM[:])
	ctx := ctxTestThread.Context
	RestoreIRQs(daif)

	if ctx.TLSG != s(13) { // SaveCurrentThreadContext sets TLSG = r14 (arg 13)
		klog.Errf("[ctx-selftest] FAIL: SaveCurrentThreadContext TLSG=%#x, want r14 %#x\n", ctx.TLSG, s(13))
		panic("runContextMarshalSelfTest: SaveCurrentThreadContext TLSG not sourced from r14")
	}
	return ctx
}

// assertAllFieldsWritten panics if any ThreadContext field is still zero after a
// save — every synthetic source is nonzero, so a zero field means the save did
// not write it (the MAZ-135 omission class). Reflection makes this automatic when
// a field is added to ThreadContext.
func assertAllFieldsWritten(saver string, ctx *ThreadContext) {
	cv := reflect.ValueOf(*ctx)
	ct := cv.Type()
	for i := range ct.NumField() {
		f := cv.Field(i)
		name := ct.Field(i).Name
		var zero bool
		switch f.Kind() {
		case reflect.Uint64:
			zero = f.Uint() == 0
		case reflect.Array: // XMM
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
			klog.Errf("[ctx-selftest] FAIL: %s left ThreadContext.%s unwritten (zero)\n", saver, name)
			panic("runContextMarshalSelfTest: a context field was not populated by a save")
		}
	}
}

// fillXMMSentinel writes a nonzero pattern into xmmSaveArea so a save that copies
// it produces a nonzero ctx.XMM (a dropped XMM copy leaves it zero → caught).
//
//go:nosplit
func fillXMMSentinel() {
	for i := range xmmSaveArea {
		xmmSaveArea[i] = byte((i % 251) + 1) // 1..251, never 0
	}
}
