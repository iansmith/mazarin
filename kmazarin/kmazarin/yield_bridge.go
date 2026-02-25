//go:build !test_stubs

package main

// kmazarinYieldImpl is called from the runtime overlay (usleep, futex) to yield
// the CPU to another ready thread without going through an exception/trap.
// On x86_64 and RISC-V, this replaces INT $0x80 / EBREAK with a direct call.
// On ARM64, SVC is still used (hardware side effects required for demand paging).
//
// Implemented in yield_bridge_{arch}.s as a tail-call to YieldToReadyThread.
//
//go:nosplit
func kmazarinYieldImpl()
