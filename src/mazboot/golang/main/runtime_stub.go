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
	// g0 uses 8KB stack at top of kernel RAM
	// g.stack.lo (offset 0), g.stack.hi (offset 8), g.stackguard0 (offset 16), g.stackguard1 (offset 24)
	const G0_STACK_SIZE = 8 * 1024 // 8KB
	const G0_STACK_TOP = 0x5F000000
	const G0_STACK_BOTTOM = G0_STACK_TOP - G0_STACK_SIZE // 0x5EFFFE000

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
	mcache0PtrAddr := uintptr(0x40131408) // Address of runtime.mcache0 pointer variable

	// Store pointer to our allocated struct in runtime.mcache0
	writeMemory64(mcache0PtrAddr, uint64(mcacheStructAddr))

	// P.mcache points to our allocated struct (not the pointer variable!)
	writeMemory64(p0Addr+56, uint64(mcacheStructAddr)) // P.mcache = &mcacheStruct

	// Step 4b: Initialize mcache.alloc[] NOW (before mallocinit)
	// This must be done before mallocinit because it may allocate during init
	// mcache.alloc array starts at offset 0x30 (48) and has 136 entries
	// Each entry should point to emptymspan (runtime.emptymspan = 0x40108500)
	emptymspanAddr := uint64(0x40108500)
	allocArrayStart := mcacheStructAddr + 0x30
	for i := uintptr(0); i < 136; i++ {
		writeMemory64(allocArrayStart+i*8, emptymspanAddr)
	}

	// DEBUG: Verify initialization - check multiple entries including spanClass 47 (0x2f)
	uartPuts("DEBUG: mcache struct at 0x")
	uartPutHex64(uint64(mcacheStructAddr))
	uartPuts(", alloc[] at 0x")
	uartPutHex64(uint64(allocArrayStart))
	uartPuts("\r\n")

	uartPuts("DEBUG: mcache.alloc[0] = 0x")
	val := readMemory64(allocArrayStart)
	uartPutHex64(val)
	uartPuts("\r\n")

	uartPuts("DEBUG: mcache.alloc[47] = 0x")
	val47 := readMemory64(allocArrayStart + 47*8)
	uartPutHex64(val47)
	uartPuts("\r\n")

	uartPuts("DEBUG: mcache.alloc[135] = 0x")
	val135 := readMemory64(allocArrayStart + 135*8)
	uartPutHex64(val135)
	uartPuts(" (should all be 0x40108500)\r\n")

	// DEBUG: Check emptymspan struct contents at critical offsets
	// The refill function loads halfwords from offset 50 and 96 and compares them
	// These must match for the emptymspan check to pass
	uartPuts("DEBUG: emptymspan struct analysis:\r\n")
	uartPuts("  emptymspan addr = 0x")
	uartPutHex64(emptymspanAddr)
	uartPuts("\r\n")
	uartPuts("  offset 50 (halfword) = 0x")
	val50 := readMemory16(uintptr(emptymspanAddr) + 50)
	uartPutHex64(uint64(val50))
	uartPuts("\r\n")
	uartPuts("  offset 96 (halfword) = 0x")
	val96 := readMemory16(uintptr(emptymspanAddr) + 96)
	uartPutHex64(uint64(val96))
	uartPuts("\r\n")
	uartPuts("  offset 88 (word, sweepgen) = 0x")
	val88 := readMemory32(uintptr(emptymspanAddr) + 88)
	uartPutHex64(uint64(val88))
	uartPuts("\r\n")
	// Also check mheap_.sweepgen for comparison
	mheapAddr := uintptr(0x40117f20) // runtime.mheap_
	uartPuts("  mheap_.sweepgen (offset 0x10140) = 0x")
	heapSweepgen := readMemory32(mheapAddr + 0x10140)
	uartPutHex64(uint64(heapSweepgen))
	uartPuts("\r\n")

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
	uartPuts("initGoHeap: Starting Go runtime heap initialization...\r\n")

	// Step 1: Set physPageSize = 4096
	// This is normally set by sysauxv() from AT_PAGESZ in the auxiliary vector.
	// Without this, mallocinit() will throw "failed to get system page size"
	physPageSizeAddr := asm.GetPhysPageSizeAddr()
	uartPuts("initGoHeap: physPageSize addr = 0x")
	uartPutHex64(uint64(physPageSizeAddr))
	uartPuts("\r\n")

	// Store 4096 to physPageSize
	writeMemory64(physPageSizeAddr, 4096)
	uartPuts("initGoHeap: Set physPageSize = 4096\r\n")

	// Step 2: Call mallocinit (not full schedinit)
	// This initializes just the heap allocator, avoiding the OS-dependent
	// parts of schedinit (godebug parsing, goargs, goenvs, etc.)
	uartPuts("initGoHeap: Calling mallocinit...\r\n")
	asm.CallMallocinit()
	uartPuts("initGoHeap: mallocinit returned!\r\n")

	// Note: mcache struct was already allocated and initialized in initRuntimeStubs
	// (before mallocinit) because mallocinit may trigger allocations that need mcache ready

	// DEBUG: Check if mallocinit changed any values
	emptymspanAddr := uintptr(0x40108500)
	mcacheStructAddr := uintptr(0x41020000) // Our allocated mcache struct
	allocArrayStart := mcacheStructAddr + 0x30
	mheapAddr := uintptr(0x40117f20)

	uartPuts("initGoHeap: POST-mallocinit values:\r\n")
	uartPuts("  mcache.alloc[0] = 0x")
	uartPutHex64(readMemory64(allocArrayStart))
	uartPuts("\r\n")
	uartPuts("  mcache.alloc[47] = 0x")
	uartPutHex64(readMemory64(allocArrayStart + 47*8))
	uartPuts("\r\n")
	uartPuts("  emptymspan offset 50 = 0x")
	uartPutHex64(uint64(readMemory16(emptymspanAddr + 50)))
	uartPuts("\r\n")
	uartPuts("  emptymspan offset 96 = 0x")
	uartPutHex64(uint64(readMemory16(emptymspanAddr + 96)))
	uartPuts("\r\n")
	uartPuts("  emptymspan offset 88 (sweepgen) = 0x")
	uartPutHex64(uint64(readMemory32(emptymspanAddr + 88)))
	uartPuts("\r\n")
	uartPuts("  mheap_.sweepgen (offset 0x10140) = 0x")
	uartPutHex64(uint64(readMemory32(mheapAddr + 0x10140)))
	uartPuts("\r\n")

	uartPuts("initGoHeap: Go runtime heap initialization complete.\r\n")
}
