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
// does NOT change the saver. It builds synthetic inputs with a distinct nonzero
// sentinel per field, points the resolve-globals (savedExcFSBase TLS-g, the
// per-exception-frame XMM slot at framePtr-256) at sentinels, swaps the current
// thread to a scratch, and calls the REAL SaveContextFromFrame. It then asserts
// every ThreadContext field is nonzero — i.e. the save actually wrote it,
// catching the MAZ-135 omission class (a field added to the struct but missed by
// the save site) — and that the dual-home TLSG is sourced correctly. frameaudit
// catches the same omission at build time by symbol presence; this catches it by
// value flow, and also catches a wrong-source wiring that presence cannot see.
//
// The global swaps run IRQ-masked at a quiescent boot point (only thread 0 exists);
// every global is restored before the reflection assertions (which allocate, so
// they run after IRQs are restored). Gated by kernel.toml ctx_marshal_test.
func runContextMarshalSelfTest() {
	frameCtx := saveFromSyntheticFrame()

	assertAllFieldsWritten("SaveContextFromFrame", &frameCtx)

	klog.Criticalf("[CTX]", "[ctx-selftest] OK — amd64 SaveContextFromFrame populates all %d ThreadContext fields; dual-home TLSG verified\n",
		reflect.TypeFor[ThreadContext]().NumField())
}

const (
	ctxFrameSentinel = 0x5A5A5A5A_00000010 // base for synthetic exception-frame slots
	ctxTLSSentinel   = 0x71170000_DEADBEEF // synthetic user TLS-g
)

// saveFromSyntheticFrame runs the REAL SaveContextFromFrame against a user-mode
// synthetic frame and returns the resulting ThreadContext. Verifies the dual-home
// TLSG is read from the user TLS slot (not R14).
//
// MAZ-139 item 2: the production XMM source is the per-exception-frame slot at
// framePtr-256, not the global xmmSaveArea. We mirror that here with a contiguous
// backing buffer whose first 256 bytes are the XMM slot and whose [21]uint64 GPR
// frame follows immediately after — so framePtr-256 lands exactly on the slot.
func saveFromSyntheticFrame() ThreadContext {
	// backing layout: [256-byte XMM slot][21 × uint64 GPR/CPU frame].
	// 256 bytes = 32 uint64, so the frame base is at index 32.
	var backing [32 + 21]uint64
	slot := (*[256]byte)(unsafe.Pointer(&backing[0]))
	frame := backing[32:] // frame[0] == &backing[0] + 256
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[17] = userCS // CS → user branch (the dual-home TLS-read path)

	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	oldFSBase := savedExcFSBase

	ctxTestTLS[0] = ctxTLSSentinel
	savedExcFSBase = uint64(uintptr(unsafe.Pointer(&ctxTestTLS[1]))) // base-8 == &ctxTestTLS[0]
	// Fill the per-frame XMM slot with a nonzero sentinel so a save that copies
	// it produces a nonzero ctx.XMM (a dropped XMM copy leaves it zero → caught).
	for i := range slot {
		slot[i] = byte((i % 251) + 1) // 1..251, never 0
	}

	ctxTestThread.Context = ThreadContext{} // zero ⇒ "unwritten stays zero"
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(uintptr(unsafe.Pointer(&frame[0])))
	SetCurrentThreadGlobal(oldThread)

	savedExcFSBase = oldFSBase
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
