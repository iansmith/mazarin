//go:build arm64

package main

// ReadThreadRAX is a stub for ARM64. The real implementation lives in
// ipc_bridge_amd64.go and reads the x86_64 RAX register from a thread's
// saved context. This diagnostic exists because x86_64 memmove uses MOVOU
// (SSE 128-bit) instructions whose XMM operands must survive page fault →
// context switch → reschedule; RAX holds the target address for the retried
// store. ARM64 does not have this failure mode (no XMM registers, and
// LDP/STP use GPRs which are already saved per-thread), so the diagnostic
// returns a sentinel value.
//
//go:noinline
func ReadThreadRAX(pid int16, tid int32) uint64 {
	return 0
}
