//go:build riscv64

package main

import "mazzy/shared/constants"

// dtbVirtAddr converts a DTB physical address to its kernel virtual address.
// On RISC-V, the DTB at PA 0xFFE00000 is in the 3-4GB range, accessible via
// the linear map at VA = PA + KernelMMIOOffset. The identity map only covers
// diplomat's low memory, not the full 4GB range.
func dtbVirtAddr(phys uint64) uintptr {
	return uintptr(phys) + constants.KernelMMIOOffset
}
