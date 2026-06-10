//go:build amd64

package main

import "unsafe"

// Resume-RIP guard (MAZ-136).
//
// A kernel-mode (CS=kernelCS) context must resume inside the kernel's own .text.
// A corrupted or stale saved RIP — observed as a stack address under TCG and as
// a demand-loaded .maz code address under KVM — is otherwise IRETQ'd into and
// executed at ring 0, silently, for millions of instructions, until it faults
// far from the cause. Under KVM that bad RIP lands in a .maz frame whose blit
// loop hammers the unmapped MMIO just past the VGA VRAM BAR: that is the MAZ-136
// "EPT_MISCONFIG storm", a symptom of this corruption rather than a bug of its
// own. Bounding the resume RIP converts the silent corruption into an immediate,
// fully-located panic at the moment a bad context is about to be resumed.
//
// kernelTextStart / kernelTextEnd alias the linker-synthesized runtime.text /
// runtime.etext markers, so the bound is exact for whatever address the kernel
// was linked at (no hard-coded range).

//go:linkname kernelTextStart runtime.text
var kernelTextStart [0]byte

//go:linkname kernelTextEnd runtime.etext
var kernelTextEnd [0]byte

// badResumeRIP reports whether next's saved RIP is implausible for resumption.
// Kernel-mode contexts must resume within kernel .text; user-mode contexts only
// fail the cheap impossible value RIP==0 (any user address is plausible). The
// scheduler panics and halts when this returns true.
//
//go:nosplit
func badResumeRIP(next *Thread) bool {
	rip := next.Context.RIP
	if next.Context.CS == kernelCS {
		lo := uint64(uintptr(unsafe.Pointer(&kernelTextStart)))
		hi := uint64(uintptr(unsafe.Pointer(&kernelTextEnd)))
		return rip < lo || rip >= hi
	}
	return rip == 0
}
