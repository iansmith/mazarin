package main

// Minimal runtime stubs to make write barrier work in bare-metal
// Based on analysis of gcWriteBarrier disassembly:
// - x28 must point to a goroutine (g) structure
// - g.m (offset 48) must point to a machine (m) structure
// - m contains a pointer (offset 200) to write barrier buffer structure
// - wbBufStruct contains bufPtr (offset 5272) and bufEnd (offset 5280)

// initRuntimeStubs initializes minimal runtime structures for write barrier
// This must be called before any pointer assignments to globals
// Addresses from target-nm: runtime.g0 at 0x331a00, runtime.m0 at 0x332100
//
//go:nosplit
func initRuntimeStubs() {
	g0Addr := uintptr(0x40100ce0) // runtime.g0 (from target-readelf -s, verified)
	m0Addr := uintptr(0x401013e0) // runtime.m0 (from target-readelf -s, verified)

	// Initialize g0 stack bounds so compiler stack checks pass
	// g0 uses 8KB stack at top of kernel RAM
	// g.stack.lo (offset 0), g.stack.hi (offset 8), g.stackguard0 (offset 16), g.stackguard1 (offset 24)
	const G0_STACK_SIZE = 8 * 1024 // 8KB
	const G0_STACK_TOP = 0x5F000000
	const G0_STACK_BOTTOM = G0_STACK_TOP - G0_STACK_SIZE // 0x5EFFFE000

	writeMemory64(g0Addr+0, uint64(G0_STACK_BOTTOM))
	writeMemory64(g0Addr+8, uint64(G0_STACK_TOP))
	writeMemory64(g0Addr+16, uint64(G0_STACK_BOTTOM+_StackGuard))
	writeMemory64(g0Addr+24, uint64(G0_STACK_BOTTOM+_StackGuard))

	// Step 1: Set g0.m = m0 (offset 48 = 0x30)
	// This is what gcWriteBarrier reads: ldr x0, [x28, #48]
	g0mAddr := g0Addr + 48
	writeMemory64(g0mAddr, uint64(m0Addr))

	// Step 2: Allocate write barrier buffer structure
	// We'll allocate it at 0x600000 (safe address, well above kernel)
	wbBufStructAddr := uintptr(0x600000)

	// Step 3: Allocate write barrier buffer (large enough to never fill)
	// Buffer will be at 0x601000, size 64KB
	wbBufStart := uintptr(0x601000)
	wbBufSize := uintptr(64 * 1024) // 64KB should be more than enough
	wbBufEnd := wbBufStart + wbBufSize

	// Step 4: Set up wbBufStruct
	// bufPtr (offset 5272 = 0x1498): current write position
	writeMemory64(wbBufStructAddr+0x1498, uint64(wbBufStart))
	// bufEnd (offset 5280 = 0x14A0): end of buffer
	writeMemory64(wbBufStructAddr+0x14A0, uint64(wbBufEnd))

	// Step 5: Set m0.wbBufStruct = wbBufStructAddr (offset 200 = 0xC8)
	// This is what gcWriteBarrier reads: ldr x0, [x0, #200]
	m0wbBufStructAddr := m0Addr + 200
	writeMemory64(m0wbBufStructAddr, uint64(wbBufStructAddr))

	// Step 6: Enable write barrier flag so gcWriteBarrier gets called
	// NOTE: This doesn't work reliably from Go because writeMemory32 may trigger
	// its own write barrier check (recursion). The flag is set in assembly instead
	// (in lib.s, before calling KernelMain)
	// Address: runtime.zerobase + 704 = 0x3582C0
	// writeBarrierFlagAddr := uintptr(0x3582C0)
	// writeMemory32(writeBarrierFlagAddr, 1) // This doesn't work reliably

	// Step 7: Set x28 register to point to g0
	// This must be done in assembly (lib.s) since we can't set registers from Go
	// We've already done this in lib.s before calling KernelMain
}
