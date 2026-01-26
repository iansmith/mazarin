// diplomat/main/uefi_mem.go - UEFI memory allocation wrappers
//
// Provides Go wrappers around UEFI Boot Services memory functions.
// Used by mmap to allocate actual physical memory.

package main

// UEFIAllocatePages allocates pages via UEFI Boot Services
// Returns the allocated physical address or 0 on failure
//
// Parameters:
//   allocType - AllocateAnyPages, AllocateMaxAddress, or AllocateAddress
//   memoryType - EfiLoaderData, EfiConventionalMemory, etc.
//   pages - number of 4KB pages to allocate
//   memory - pointer to uint64 (in/out parameter)
//            For AllocateAddress: input is desired address
//            For all types: output is allocated address
//
//go:nosplit
func UEFIAllocatePages(allocType uint32, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}

	bs := systemTable.BootServices
	if bs.AllocatePages == 0 {
		return EFI_UNSUPPORTED
	}

	// Call UEFI AllocatePages via assembly helper
	// Status AllocatePages(IN AllocType, IN MemoryType, IN Pages, IN OUT *PhysicalAddress)
	status := uefiCallAllocatePages(bs.AllocatePages, allocType, memoryType, pages, memory)
	return status
}

// UEFIFreePages frees pages allocated via AllocatePages
//
//go:nosplit
func UEFIFreePages(memory uint64, pages uint64) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}

	bs := systemTable.BootServices
	if bs.FreePages == 0 {
		return EFI_UNSUPPORTED
	}

	// Call UEFI FreePages
	// Status FreePages(IN PhysicalAddress, IN Pages)
	status := uefiCallFreePages(bs.FreePages, memory, pages)
	return status
}

// AllocatePagesForMmap allocates memory for mmap using UEFI
// This is called by DiplomatMmap when UEFI Boot Services are available
//
//go:nosplit
func AllocatePagesForMmap(addr uintptr, size uintptr, fixed bool) (uintptr, bool) {
	// Calculate number of pages needed (round up)
	pages := (uint64(size) + 4095) / 4096

	var memory uint64
	var allocType uint32
	var status EFI_STATUS

	if fixed {
		// MAP_FIXED: try to allocate at specific address
		memory = uint64(addr)
		allocType = AllocateAddress
		status = UEFIAllocatePages(allocType, EfiLoaderData, pages, &memory)

		if status != EFI_SUCCESS {
			return 0, false // Failed to allocate at requested address
		}
		return uintptr(memory), true
	}

	// Not fixed - try hint first if provided
	if addr != 0 {
		memory = uint64(addr)
		allocType = AllocateAddress
		status = UEFIAllocatePages(allocType, EfiLoaderData, pages, &memory)

		if status == EFI_SUCCESS {
			return uintptr(memory), true // Hint worked
		}
		// Hint failed - fall through to any pages
	}

	// Allocate any pages
	memory = 0
	allocType = AllocateAnyPages
	status = UEFIAllocatePages(allocType, EfiLoaderData, pages, &memory)

	if status != EFI_SUCCESS {
		return 0, false
	}

	return uintptr(memory), true
}

// uefiCallAllocatePages is implemented in assembly (uefi_calls.s)
// Calls UEFI AllocatePages using MS x64 calling convention
func uefiCallAllocatePages(funcPtr uintptr, allocType uint32, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS

// uefiCallFreePages is implemented in assembly (uefi_calls.s)
// Calls UEFI FreePages using MS x64 calling convention
func uefiCallFreePages(funcPtr uintptr, memory uint64, pages uint64) EFI_STATUS
