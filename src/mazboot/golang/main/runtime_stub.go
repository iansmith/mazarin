package main

import (
	"mazboot/asm"
	"unsafe"
)

// Minimal runtime stubs to make write barrier work in bare-metal
// Based on analysis of gcWriteBarrier disassembly:
// - x28 must point to a goroutine (g) structure
// - g.m (offset 48) must point to a machine (m) structure
// - m contains a pointer (offset 200) to write barrier buffer structure
// - wbBufStruct contains bufPtr (offset 5272) and bufEnd (offset 5280)

// initRuntimeStubs initializes minimal runtime structures for write barrier
// This must be called before any pointer assignments to globals
//
//go:nosplit
func initRuntimeStubs() {
	// Get addresses from assembly functions that use linker symbols
	// This avoids hardcoding addresses that change with each build
	g0Addr := asm.GetG0Addr()
	m0Addr := asm.GetM0Addr()

	// Initialize g0 stack bounds so compiler stack checks pass
	// g0 uses 64KB stack at top of kernel RAM (matches real Go runtime)
	// g.stack.lo (offset 0), g.stack.hi (offset 8), g.stackguard0 (offset 16), g.stackguard1 (offset 24)
	const G0_STACK_SIZE = 64 * 1024 // 64KB (matches runtime/asm_arm64.s)
	const G0_STACK_TOP = 0x5F000000
	const G0_STACK_BOTTOM = G0_STACK_TOP - G0_STACK_SIZE // 0x5EFF0000

	writeMemory64(g0Addr+0, uint64(G0_STACK_BOTTOM))
	writeMemory64(g0Addr+8, uint64(G0_STACK_TOP))
	writeMemory64(g0Addr+16, uint64(G0_STACK_BOTTOM+_StackGuard))
	writeMemory64(g0Addr+24, uint64(G0_STACK_BOTTOM+_StackGuard))

	// CRITICAL: Set g0.sched.sp so morestack can switch to g0's stack
	// When morestack is called, it switches to g0's stack using g0.sched.sp
	// If this is not set (0), SP becomes 0 and we crash with a data abort
	// g.sched is at offset returned by unsafe.Offsetof, sched.sp is first field
	// Set it to near the top of g0's stack (16-byte aligned, with room for frame)
	g0SchedOffset := unsafe.Offsetof(runtimeG{}.sched)
	g0SchedSpAddr := g0Addr + g0SchedOffset // g0.sched.sp (first field of gobuf)
	g0StackTopAligned := (G0_STACK_TOP - 64) &^ 0xF // Leave 64 bytes, align to 16
	writeMemory64(g0SchedSpAddr, uint64(g0StackTopAligned))

	// Step 1: Set g0.m = m0
	// g.m is at offset 48 (after stack:16 + stackguard0:8 + stackguard1:8 + _panic:8 + _defer:8)
	// This is what gcWriteBarrier reads: ldr x0, [x28, #48]
	// Using unsafe.Offsetof for correctness
	g0mOffset := unsafe.Offsetof(runtimeG{}.m)
	writeMemory64(g0Addr+g0mOffset, uint64(m0Addr))

	// Step 1b: Set m0.g0 = g0 (offset 0 of m struct)
	// This is CRITICAL for runtime.systemstack to work!
	// Without this, systemstack says "called from unexpected goroutine"
	writeMemory64(m0Addr+0, uint64(g0Addr))

	// Step 1c: Set m0.curg = g0 initially
	// This will be updated when we switch to a different goroutine
	// Use unsafe.Offsetof for correctness (curg offset changed after struct fixes)
	m0CurgOffset := unsafe.Offsetof(runtimeM{}.curg)
	writeMemory64(m0Addr+m0CurgOffset, uint64(g0Addr))

	// Step 2: Create a minimal P (processor) structure
	// m.p (at offset 200) points to P, which contains:
	//   - Offset 56: mcache pointer (points to mcache0)
	//   - Offset 5272: wbBuf.next (write barrier buffer current position)
	//   - Offset 5280: wbBuf.end (write barrier buffer end)
	// CRITICAL: Must be within mapped bootloader RAM (0x40100000-0x78000000)
	// Using 0x41000000 (16MB into RAM, safely above kernel code)
	p0Addr := uintptr(0x41000000)

	// Step 3: Allocate write barrier buffer (large enough to never fill)
	// Buffer will be at 0x41010000 (64KB after P struct, page-aligned), size 64KB
	wbBufStart := uintptr(0x41010000)
	wbBufSize := uintptr(64 * 1024) // 64KB should be more than enough
	wbBufEnd := wbBufStart + wbBufSize

	// Step 4: Set up P structure and allocate mcache struct
	//
	// CRITICAL FIX: runtime.mcache0 is a POINTER variable (8 bytes), not the
	// actual mcache struct! We must allocate a proper mcache struct and store
	// its pointer in runtime.mcache0, otherwise we corrupt other global variables.
	//
	// mcache struct is ~0x470 bytes (0x30 header + 136 * 8 byte alloc array)
	// Allocate 0x500 bytes at 0x41020000 (after wbBuf which ends at 0x41020000)
	mcacheStructAddr := uintptr(0x41020000)
	mcache0PtrAddr := asm.GetMcache0Addr() // Get address dynamically via linker symbol

	// Store pointer to our allocated struct in runtime.mcache0
	writeMemory64(mcache0PtrAddr, uint64(mcacheStructAddr))

	// P.mcache points to our allocated struct (not the pointer variable!)
	writeMemory64(p0Addr+56, uint64(mcacheStructAddr)) // P.mcache = &mcacheStruct

	// Step 4b: Initialize mcache.alloc[] NOW (before mallocinit)
	// This must be done before mallocinit because it may allocate during init
	// mcache.alloc array starts at offset 0x30 (48) and has 136 entries
	// Each entry should point to emptymspan - get address dynamically via linker symbol
	emptymspanAddr := uint64(asm.GetEmptymspanAddr())
	allocArrayStart := mcacheStructAddr + 0x30
	for i := uintptr(0); i < 136; i++ {
		writeMemory64(allocArrayStart+i*8, emptymspanAddr)
	}

	// wbBuf.next (offset 5272 = 0x1498): current write position
	writeMemory64(p0Addr+0x1498, uint64(wbBufStart))
	// wbBuf.end (offset 5280 = 0x14A0): end of buffer
	writeMemory64(p0Addr+0x14A0, uint64(wbBufEnd))

	// Step 5: Set m0.p = p0Addr (offset 200 = 0xC8)
	// This is what gcWriteBarrier and getMCache read: ldr x0, [x0, #200]
	writeMemory64(m0Addr+200, uint64(p0Addr))

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

// initGoHeap initializes the Go runtime heap allocator.
// This must be called after initRuntimeStubs() and after basic debug systems
// (UART, framebuffer) are initialized, but before any heap allocation.
//
// It sets physPageSize and calls runtime.mallocinit() directly to
// initialize just the heap allocator (not the full scheduler).
//
//go:nosplit
func initGoHeap() {
	// Set physPageSize = 4096 (normally set by sysauxv from AT_PAGESZ)
	physPageSizeAddr := asm.GetPhysPageSizeAddr()
	writeMemory64(physPageSizeAddr, 4096)

	// Call mallocinit to initialize heap allocator
	asm.CallMallocinit()
}
