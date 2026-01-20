// Package ksyscall provides argument validation helpers for syscalls.
// These check that pointers/addresses are in appropriate memory regions
// of the calling priest/program.
package ksyscall

import "kmazarin/kmem"

// kernelVABase is the start of kernel virtual address space (TTBR1).
// Addresses at or above this are kernel-only.
const kernelVABase = uint64(0xFFFF000000000000)

// isInValidMemory checks if ptr points to accessible memory.
// Returns false if ptr is null, unmapped, or in kernel space.
// Used for: filename pointers, buffer pointers, etc.
//
//go:nosplit
func isInValidMemory(ptr uintptr) bool {
	if ptr == 0 {
		return false
	}
	// Check against kernel space (TTBR1 range)
	if uint64(ptr) >= kernelVABase {
		return false
	}
	// Check page tables to ensure page is mapped and readable
	pa := kmem.WalkUserPageTable(ptr)
	return pa != 0
}

// isInExecSegment checks if addr is within the caller's executable code segment.
// Returns false if addr is not in a region with execute permission.
// Used for: function pointers, callback addresses, etc.
//
// For now, we check that the address is in userspace and the page is mapped.
// TODO: Actually check execute permission bits in PTE.
//
//go:nosplit
func isInExecSegment(addr uintptr) bool {
	if addr == 0 {
		return false
	}
	// Check against kernel space (TTBR1 range)
	if uint64(addr) >= kernelVABase {
		return false
	}
	// Check page is mapped
	pa := kmem.WalkUserPageTable(addr)
	if pa == 0 {
		return false
	}
	// TODO: Get PTE and check UXN bit (should be 0 for executable)
	// For now, just verify the page is mapped
	return true
}

// isInWritableData checks if ptr points to caller's writable memory.
// Uses page table lookup rather than tracking specific regions.
//
// Why not track stack bounds?
// Go dynamically grows stacks - when a goroutine needs more stack,
// the runtime allocates a new larger stack (often in heap), copies
// data, and adjusts pointers. The kernel has no visibility into this.
//
// Instead, we check the page table directly:
// 1. Is the address in userspace (not kernel)?
// 2. Is the page mapped?
// 3. Does the page have write permission?
//
// This handles all cases: heap, data, BSS, stack (wherever it is),
// and any memory Go's runtime has allocated and mapped as writable.
//
//go:nosplit
func isInWritableData(ptr uintptr) bool {
	if ptr == 0 {
		return false
	}

	// Must be in userspace (TTBR0 range), not kernel space (TTBR1)
	if uint64(ptr) >= kernelVABase {
		return false
	}

	// Check page table for this address
	pa := kmem.WalkUserPageTable(ptr)
	if pa == 0 {
		return false // Page not mapped
	}

	// TODO: Get PTE and check AP bits for write permission
	// On ARM64: AP[2:1] bits control access permissions
	// User writable = AP[2:1] = 01 (RW at EL0)
	// For now, if page is mapped in userspace, assume writable
	return true
}

// ValidateFilenamePtr validates a filename pointer argument.
// Returns encoded error or 0 on success.
//
//go:nosplit
func ValidateFilenamePtr(ptr uint64) uint64 {
	if !isInValidMemory(uintptr(ptr)) {
		return encodeError(majorBadArg, minorNotAccessibleMem)
	}
	return 0
}

// ValidateExecAddr validates an executable address argument.
// Returns encoded error or 0 on success.
//
//go:nosplit
func ValidateExecAddr(addr uint64) uint64 {
	if !isInExecSegment(uintptr(addr)) {
		return encodeError(majorBadArg, minorNotInExec)
	}
	return 0
}

// ValidateWritablePtr validates a writable data pointer argument.
// Accepts heap, data, BSS, or stack pointers.
// Returns encoded error or 0 on success.
//
//go:nosplit
func ValidateWritablePtr(ptr uint64) uint64 {
	if !isInWritableData(uintptr(ptr)) {
		return encodeError(majorBadArg, minorNotWritableData)
	}
	return 0
}

// readNullTerminatedString reads a null-terminated string from userspace memory.
// Used to read filename arguments from userspace.
// Returns empty string if ptr is null or memory is not accessible.
//
//go:nosplit
func readNullTerminatedString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	maxLen := 256
	buf := make([]byte, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		b, ok := kmem.ReadUserByte(ptr + uintptr(i))
		if !ok {
			return "" // Memory not readable
		}
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}
