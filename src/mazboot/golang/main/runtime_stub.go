package main

import (
	"mazboot/asm"
	"unsafe"
)


// setTPIDR_EL1 sets the TPIDR_EL1 register (kernel TLS pointer)
//
//go:nosplit
func setTPIDR_EL1(addr uintptr) {
	asm.WriteTpidrEl1(uint64(addr))
}

// patchRuntimeTLSForEL1 patches the Go runtime's TLS functions to use TPIDR_EL1 instead of TPIDR_EL0
// This is necessary because:
// 1. We're running the kernel at EL1 (not EL0)
// 2. User-space programs will need TPIDR_EL0 for their own TLS
// 3. Using TPIDR_EL1 for kernel allows true SMP without TLS conflicts
//
// The patch is simple: Change MRS TPIDR_EL0, R0 (0xd53bd040) to MRS TPIDR_EL1, R0 (0xd53bd041)
// This is a one-bit change in the instruction encoding (last nibble 0->1)
//
//go:nosplit
func patchRuntimeTLSForEL1() {
	// ARM64 instruction encodings:
	// MRS TPIDR_EL0, R0 = 0xd53bd040  (read user TLS)
	// MRS TPIDR_EL1, R0 = 0xd53bd041  (read kernel TLS)
	// MSR TPIDR_EL0, R0 = 0xd51bd040  (write user TLS)
	// MSR TPIDR_EL1, R0 = 0xd51bd041  (write kernel TLS)
	const (
		MRS_TPIDR_EL0_R0 = uint32(0xd53bd040)
		MRS_TPIDR_EL1_R0 = uint32(0xd53bd041)
		MSR_TPIDR_EL0_R0 = uint32(0xd51bd040)
		MSR_TPIDR_EL1_R0 = uint32(0xd51bd041)
	)

	// Get addresses of the two specific functions we need to patch
	loadGAddr := asm.GetRuntimeLoadGAddr()
	saveGAddr := asm.GetRuntimeSaveGAddr()

	patchCount := 0

	// Patch runtime.load_g - scan up to 64 instructions (256 bytes)
	for offset := uintptr(0); offset < 256; offset += 4 {
		instrAddr := loadGAddr + offset
		instr := readMemory32(instrAddr)
		if instr == MRS_TPIDR_EL0_R0 {
			// Found it! Patch from EL0 to EL1 (just change last nibble from 0 to 1)
			writeMemory32(instrAddr, MRS_TPIDR_EL1_R0)
			asm.CleanDataCacheVA(instrAddr)
			patchCount++
			break // Only one instruction per function
		}
	}

	// Patch runtime.save_g - scan up to 64 instructions (256 bytes)
	for offset := uintptr(0); offset < 256; offset += 4 {
		instrAddr := saveGAddr + offset
		instr := readMemory32(instrAddr)
		if instr == MRS_TPIDR_EL0_R0 {
			// Found it! Patch from EL0 to EL1 (just change last nibble from 0 to 1)
			writeMemory32(instrAddr, MRS_TPIDR_EL1_R0)
			asm.CleanDataCacheVA(instrAddr)
			patchCount++
			break // Only one instruction per function
		}
	}

	// Invalidate instruction cache once after all patches
	if patchCount > 0 {
		asm.InvalidateInstructionCacheAll()
		asm.Isb()
	}
}

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

	// CRITICAL STEP 0: Initialize TPIDR_EL0 FIRST so Go runtime can access TLS
	// We do this BEFORE patching because print() needs TLS to work!
	// Later we'll patch to use TPIDR_EL1, but for now use EL0 to get print working
	const tlsBlockAddr = uintptr(0x41030000) // TLS block at fixed address
	writeMemory64(tlsBlockAddr, uint64(g0Addr)) // Store g0 pointer at TLS offset 0
	asm.WriteTpidrEl0(uint64(tlsBlockAddr))      // Set TPIDR_EL0 temporarily

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

	// Step 1c: Set m0.curg = NULL initially
	// CRITICAL: m.curg means "machine's current USER goroutine"
	// It should NEVER point to g0 (which is the system goroutine)!
	// When on g0, m.curg should be NULL (no user goroutine) or point to suspended user goroutine
	// This will be set when we create and switch to the main goroutine
	// Use unsafe.Offsetof for correctness (curg offset changed after struct fixes)
	m0CurgOffset := unsafe.Offsetof(runtimeM{}.curg)
	writeMemory64(m0Addr+m0CurgOffset, 0) // NULL, not g0!

	// Step 2: Create a properly initialized P (processor) structure at 0x41000000
	// In c-archive mode, mallocinit() may call gcmarknewobject which needs M.p to be valid
	// The real runtime in exe mode doesn't need this, but c-archive mode behaves differently
	p0Addr := uintptr(0x41000000)

	// Step 3: Initialize P0 fields (manually replicating runtime.(*p).init(0))
	// CRITICAL fields that must be set:
	writeMemory32(p0Addr+0, 0)  // P.id = 0
	writeMemory32(p0Addr+4, 2)  // P.status = _Pgcstop (2)

	// Step 4: Set up mcache0
	mcacheStructAddr := uintptr(0x41020000)
	mcache0PtrAddr := asm.GetMcache0Addr()
	writeMemory64(mcache0PtrAddr, uint64(mcacheStructAddr))
	writeMemory64(p0Addr+56, uint64(mcacheStructAddr)) // P.mcache at offset 56

	// Step 4b: Initialize mcache.alloc[] with emptymspan
	// mcache.alloc is at offset 0x30 (48 bytes) and has 136 entries
	emptymspanAddr := uint64(asm.GetEmptymspanAddr())
	allocArrayStart := mcacheStructAddr + 0x30
	for i := uintptr(0); i < 136; i++ {
		writeMemory64(allocArrayStart+i*8, emptymspanAddr)
	}

	// Step 5: Initialize write barrier buffer (wbBuf.reset())
	// wbBuf is embedded in P structure, need to calculate buffer address
	// P.wbBuf at offset 0x1490, wbBuf.next at +0, wbBuf.end at +8, wbBuf.buf at +16
	wbBufAddr := p0Addr + 0x1490
	wbBufBufStart := wbBufAddr + 16 // Start of buf array
	wbBufBufSize := 512 * 8         // 512 entries * 8 bytes each = 4096 bytes
	writeMemory64(wbBufAddr+0, uint64(wbBufBufStart))                 // wbBuf.next = start of buf
	writeMemory64(wbBufAddr+8, uint64(wbBufBufStart+uintptr(wbBufBufSize))) // wbBuf.end = end of buf

	// Step 6: Set m0.p = p0Addr
	writeMemory64(m0Addr+200, uint64(p0Addr))

	// Step 7: Set p.m = m0 to complete bidirectional binding
	writeMemory64(p0Addr+0x30, uint64(m0Addr)) // P.m at offset 0x30

	// Step 8: Now that runtime structures are set up, patch TLS to use TPIDR_EL1
	patchRuntimeTLSForEL1()

	// Step 9: Switch from TPIDR_EL0 to TPIDR_EL1 now that functions are patched
	setTPIDR_EL1(tlsBlockAddr) // Set TPIDR_EL1 to TLS block (kernel TLS)

	// That's all! schedinit() will handle the rest:
	// - mallocinit() will use our mcache0
	// - procresize() will find M0.p already set and reuse this P0
}

// tryPreempt attempts to preempt the current goroutine if one is running.
// Called from timer interrupt handler to trigger scheduler preemption.
//
// This implements the logic of runtime.preemptone() directly:
// - Set g.preempt = true
// - Set g.stackguard0 = stackPreempt (0xfffffffffffffade)
//
// The stackPreempt value causes the next function call to trigger morestack,
// which checks for preemption and calls the scheduler.
//
//go:nosplit
//go:noinline
func tryPreempt() {
	const stackPreempt = uintptr(0xfffffffffffffade) // Special value from runtime

	// Get current g from TLS (TPIDR_EL1)
	gAddr := asm.GetCurrentG()
	if gAddr == 0 {
		return // No current goroutine
	}

	// Get M0 address to access M.curg
	m0Addr := asm.GetM0Addr()
	if m0Addr == 0 {
		return
	}

	// Read M.curg (current running goroutine) at offset 120
	curgAddr := readMemory64(m0Addr + 120)
	if curgAddr == 0 {
		return // No current goroutine
	}

	// Set g.preempt = true (bool at offset 0x19c in runtimeG)
	// In Go, bool is 1 byte
	writeMemory8(uintptr(curgAddr+0x19c), 1)

	// Set g.stackguard0 = stackPreempt (uintptr at offset 0x10 in runtimeG)
	writeMemory64(uintptr(curgAddr+0x10), uint64(stackPreempt))
}
