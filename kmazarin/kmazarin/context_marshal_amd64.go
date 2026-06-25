//go:build amd64

package main

import (
	"reflect"
	"unsafe"

	"mazzy/kmazarin/klog"
)

// Scratch state used ONLY by runContextMarshalSelfTest. ctxTestTLS[0] is the
// synthetic user TLS-g (savedExcFSBase-8 points at it during the test).
//
// ctxTestBacking holds the synthetic exception frame. It MUST be a package global
// (stable address), NOT a stack-local array: the tests hold framePtr as a uintptr
// across splittable calls (fillXMMSlotSentinel, SaveAndDisableIRQs,
// SetCurrentThreadGlobal), and a stack-local buffer could be relocated by morestack
// mid-test — staling framePtr so the writes and SaveContextFromFrame's reads target
// different memory (a latent boot-halting flake). A global never moves. The tests
// run sequentially at boot, so sharing one buffer is safe; each fully populates the
// slots it reads. Sized for the largest layout (per-frame extension + 21-qword frame).
var (
	ctxTestThread  Thread
	ctxTestTLS     [4]uint64
	ctxTestBacking [excFrameExtSize/8 + 21]uint64
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

	kernelCtx := saveFromSyntheticKernelFrame()
	assertAllFieldsWritten("SaveContextFromFrame(kernel)", &kernelCtx)

	checkVectorFromFrame()

	klog.Criticalf("[CTX]", "[ctx-selftest] OK — amd64 SaveContextFromFrame populates all %d ThreadContext fields; dual-home TLSG + per-frame vector verified\n",
		reflect.TypeFor[ThreadContext]().NumField())

	// MAZ-143: deterministic RED/GREEN for the (g,SP)-mismatch detector — shares
	// this gated, quiescent, thread-0-only boot point and the scratch globals.
	runGSPMismatchSelfTest()
	// MAZ-143 confirmation (a): proves a clone child's TLSG carries the parent's
	// high-canonical g, so a naive gLooksValid widening regresses clone (R14==g0 rule safe).
	runCloneTLSGSelfTest()
	// MAZ-147 Option B: deterministic RED→GREEN for the m.locks checkpoint — the
	// save hides g0's m.locks from a foreign m0 reader (RED without the fix) and
	// stashes the count+addr for the asm resume re-arm. Same gated, quiescent,
	// thread-0-only boot point; borrows the real g0.m.locks IRQ-masked.
	runMLockCheckpointSelfTest()
}

const (
	ctxFrameSentinel = 0x5A5A5A5A_00000010 // base for synthetic exception-frame slots
	ctxTLSSentinel   = 0x71170000_DEADBEEF // synthetic user TLS-g
	// Kernel-branch sentinels (both gLooksValid: nonzero, low-canonical). The
	// per-frame slot value must WIN; the frame R14 fallback must NOT be chosen.
	ctxKernelTLSGSentinel = 0x00005555_55555000 // planted in the per-exception-frame TLSG slot
	ctxKernelR14Sentinel  = 0x00006666_66666000 // frame[13] = R14 dual-home fallback (must lose)
	// Unique value that is never a real vector number (vectors are 0..255), so the
	// pre-fix read of the small global currentVector can never coincidentally match.
	ctxVectorSentinel = 0x00C0FFEE_0000002A // planted in the per-exception-frame vector slot
)

// fillXMMSlotSentinel fills the per-frame XMM slot at framePtr-256 with a nonzero
// pattern so a save that copies it produces a nonzero ctx.XMM (a dropped XMM copy
// leaves it zero → caught by assertAllFieldsWritten).
func fillXMMSlotSentinel(framePtr uintptr) {
	slot := (*[256]byte)(unsafe.Pointer(framePtr - 256))
	for i := range slot {
		slot[i] = byte((i % 251) + 1) // 1..251, never 0
	}
}

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
	backing := ctxTestBacking[:32+21] // stable global buffer — see ctxTestBacking
	frame := backing[32:]             // frame[0] == &backing[0] + 256
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[17] = userCS // CS → user branch (the dual-home TLS-read path)

	framePtr := uintptr(unsafe.Pointer(&frame[0]))
	fillXMMSlotSentinel(framePtr) // XMM slot at framePtr-256 == &backing[0]

	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	oldFSBase := savedExcFSBase

	ctxTestTLS[0] = ctxTLSSentinel
	savedExcFSBase = uint64(uintptr(unsafe.Pointer(&ctxTestTLS[1]))) // base-8 == &ctxTestTLS[0]

	ctxTestThread.Context = ThreadContext{} // zero ⇒ "unwritten stays zero"
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(framePtr)
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

// saveFromSyntheticKernelFrame runs the REAL SaveContextFromFrame against a
// KERNEL-mode synthetic frame (CS = kernelCS) and returns the resulting
// ThreadContext. It verifies the dual-home kernel TLS-g is sourced from THIS
// exception level's PER-FRAME slot (framePtr - excFrameExtSize + excFrameTLSGOff),
// not from the frame R14 fallback and not from a shared global.
//
// MAZ-139 DoD #2: savedExcKernelTLSG used to live in a single global that a
// nested exception's common_exception_entry overwrote between an outer entry's
// capture and the outer's SaveContextFromFrame consume — silently reintroducing
// the MAZ-135 "morestack on g0" wrong-dual-home-g bug. The fix captures it into
// the per-exception-frame extension. Asserting the kernel TLSG is read from the
// per-frame slot IS the clobber-resistance property: a per-frame slot is private
// to its nesting level, so nested entries write their own and cannot collide.
//
//   - RED  (pre-fix): SaveContextFromFrame reads the shared global → ctx.TLSG is
//     whatever the last entry captured (a real g, or the R14 fallback) — never
//     the per-frame sentinel → the assertion below fails.
//   - GREEN (post-fix): reads the per-frame slot → ctx.TLSG == the sentinel.
func saveFromSyntheticKernelFrame() ThreadContext {
	// backing layout: [excFrameExtSize-byte per-level extension][21 × uint64 GPR/CPU frame].
	// excFrameExtSize bytes = excFrameExtSize/8 uint64, so the frame base is at that index.
	// Within the extension: the TLSG slot is at framePtr-excFrameExtSize+excFrameTLSGOff
	// (= backing[0]); the XMM snapshot is at framePtr-256 (excFrameXMMOff within the ext).
	const extWords = excFrameExtSize / 8
	backing := ctxTestBacking[:extWords+21] // stable global buffer — see ctxTestBacking
	frame := backing[extWords:]             // frame[0] == &backing[0] + excFrameExtSize
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[13] = ctxKernelR14Sentinel // R14 — the dual-home fallback; must NOT be chosen
	frame[17] = kernelCS             // CS → kernel branch (the per-frame TLS-g read path)

	framePtr := uintptr(unsafe.Pointer(&frame[0]))
	// Plant the per-level kernel TLS-g exactly where the fixed SaveContextFromFrame
	// reads it. The buggy code ignores this slot and reads the global instead.
	*(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameTLSGOff)) = ctxKernelTLSGSentinel
	fillXMMSlotSentinel(framePtr)

	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()

	ctxTestThread.Context = ThreadContext{} // zero ⇒ "unwritten stays zero"
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(framePtr)
	SetCurrentThreadGlobal(oldThread)

	ctx := ctxTestThread.Context
	RestoreIRQs(daif)

	if ctx.TLSG != uint64(ctxKernelTLSGSentinel) {
		klog.Errf("[ctx-selftest] FAIL: kernel-branch SaveContextFromFrame TLSG=%#x, want per-frame slot sentinel %#x (savedExcKernelTLSG not read from the per-exception-frame slot — single-global nested-clobber risk, MAZ-139 DoD#2)\n",
			ctx.TLSG, uint64(ctxKernelTLSGSentinel))
		panic("runContextMarshalSelfTest: kernel TLSG not sourced from the per-exception-frame slot")
	}
	if ctx.CS != kernelCS || ctx.FSBase != kmazarinFSBase {
		klog.Errf("[ctx-selftest] FAIL: kernel-branch SaveContextFromFrame frame mapping wrong (CS/FSBase)\n")
		panic("runContextMarshalSelfTest: kernel-branch frame mapping incorrect")
	}
	return ctx
}

// checkVectorFromFrame asserts that vectorFromFrame sources the interrupt vector
// from THIS exception level's per-frame slot, not the shared global currentVector.
//
// MAZ-139 DoD #2 SLICE 2: the unhandled-fault path (HandleUnhandledExceptionAsm)
// used to take its vector from the global currentVector, which a nested exception
// would clobber between an outer entry and the outer's functional read. The fix
// stashes the vector per-frame at entry and reads it via vectorFromFrame. Asserting
// the per-frame source IS the clobber-resistance property (per-frame slots are
// private to each nesting level).
//
//   - RED  (pre-fix): vectorFromFrame returns the global (a small vector number) →
//     never the unique sentinel → fails.
//   - GREEN (post-fix): reads the per-frame slot → the sentinel.
func checkVectorFromFrame() {
	const extWords = excFrameExtSize / 8
	backing := ctxTestBacking[:extWords+21] // stable global buffer — see ctxTestBacking
	frame := backing[extWords:]
	framePtr := uintptr(unsafe.Pointer(&frame[0]))
	*(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameVecOff)) = ctxVectorSentinel

	got := vectorFromFrame(framePtr)
	if got != uint64(ctxVectorSentinel) {
		klog.Errf("[ctx-selftest] FAIL: vectorFromFrame=%#x, want per-frame vec slot sentinel %#x (currentVector not read from the per-exception-frame slot — single-global nested-clobber risk, MAZ-139 DoD#2)\n",
			got, uint64(ctxVectorSentinel))
		panic("runContextMarshalSelfTest: vector not sourced from the per-exception-frame slot")
	}
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
