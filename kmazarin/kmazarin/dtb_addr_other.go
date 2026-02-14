//go:build !riscv64

package main

// dtbVirtAddr converts a DTB physical address to its kernel virtual address.
// On ARM64 and x86_64, the DTB is in low memory that is identity-mapped,
// so the physical address can be used directly.
func dtbVirtAddr(phys uint64) uintptr {
	return uintptr(phys)
}
