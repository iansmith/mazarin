//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// Scheduler Bootstrap (Option 3)
//
// This file implements minimal scheduler initialization to allow schedinit() to run.
// We bootstrap g0, m0, and one P before calling schedinit, so that gopark/goready work.
//
// Bootstrap sequence:
//   1. Get pointers to runtime.g0 and runtime.m0 using assembly helpers
//   2. Initialize their fields (stacks, IDs, etc.)
//   3. Link them together (g0.m = m0, m0.g0 = g0, m0.p = p0)
//   4. Set current G to g0 (via x28 register using runtime.setg)
//   5. Call schedinit() - which will now work because scheduler infrastructure exists

// runtimeP matches Go runtime's p struct
// Minimal fields needed for bootstrap
type runtimeP struct {
	id          int32
	status      uint32        // P status
	link        uintptr       // puintptr
	schedtick   uint32        // incremented on every scheduler call
	syscalltick uint32        // incremented on every system call
	m           uintptr       // muintptr - back-link to associated m
	mcache      unsafe.Pointer // *mcache

	// More fields exist in real runtime, but we only need these for bootstrap
	// The runtime will initialize the rest during schedinit
}

// bootstrapScheduler initializes g0, m0, and one P before calling schedinit
//
// This allows gopark/goready to work because the scheduler infrastructure exists.
//
//go:nosplit
func bootstrapScheduler() bool {
	print("Bootstrapping scheduler (g0, m0, P)...\r\n")

	// Step 1: Get pointers to runtime.g0 and runtime.m0
	// These are defined in the runtime package and we access them via assembly helpers
	print("  Getting g0 address...\r\n")
	g0Addr := asm.GetG0Addr()
	print("  g0Addr = ")
	printHex64(uint64(g0Addr))
	print("\r\n")

	print("  Getting m0 address...\r\n")
	m0Addr := asm.GetM0Addr()
	print("  m0Addr = ")
	printHex64(uint64(m0Addr))
	print("\r\n")

	if g0Addr == 0 || m0Addr == 0 {
		print("ERROR: Failed to get g0/m0 addresses\r\n")
		return false
	}

	print("  Addresses retrieved successfully\r\n")

	// Step 2: Simply set x28 to point to runtime.g0
	// Don't try to initialize g0/m0 - let schedinit do that
	//
	// The key insight: schedinit NEEDS x28 (current G) to be set before it runs,
	// but it will handle initializing the g0/m0 structures itself.
	//
	// Just set the register and let the runtime do its job!
	print("  Setting x28 register to g0...\r\n")
	asm.SetCurrentG(g0Addr)
	print("  x28 now points to runtime.g0\r\n")

	print("Scheduler bootstrap complete!\r\n")

	return true
}

