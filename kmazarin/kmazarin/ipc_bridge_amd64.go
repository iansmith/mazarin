//go:build amd64

package main

import "mazzy/kmazarin/proc"

// ReadThreadRAX reads the saved RAX from a thread's context.
// Used by diagMmapWake (via go:linkname) to verify register state before
// waking a page-fault-blocked thread. On x86_64, memmove uses MOVOU (SSE)
// instructions that depend on XMM registers surviving the page fault →
// context switch → reschedule cycle; RAX typically holds the mmap VA that
// the retried store will write to.
//
//go:noinline
func ReadThreadRAX(pid int16, tid int32) uint64 {
	t := threadLookupByTID(tid)
	if t == nil || t.PID != proc.ShepherdId(pid) {
		return 0xDEADDEADDEADDEAD
	}
	return t.Context.RAX
}
