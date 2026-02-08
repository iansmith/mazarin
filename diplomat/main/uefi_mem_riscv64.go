//go:build riscv64

// diplomat/main/uefi_mem_riscv64.go - UEFI memory allocation wrappers (RISC-V 64-bit)
//
// Provides Go wrappers around UEFI Boot Services memory functions.
// Used by mmap to allocate actual physical memory.
//
// NOTE: This file is RISC-V-specific because the assembly helpers
// read function pointers from the global systemTable directly.

package main

import "unsafe"

// UEFIAllocatePages allocates pages via UEFI Boot Services
// Returns the allocated physical address or 0 on failure
//
// Parameters:
//
//	allocType - AllocateAnyPages, AllocateMaxAddress, or AllocateAddress
//	memoryType - EfiLoaderData, EfiConventionalMemory, etc.
//	pages - number of 4KB pages to allocate
//	memory - pointer to uint64 (in/out parameter)
//	         For AllocateAddress: input is desired address
//	         For all types: output is allocated address
//
//go:nosplit
func UEFIAllocatePages(allocType uint32, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}

	// RISC-V assembly reads BootServices->AllocatePages from global systemTable
	status := uefiAllocatePages(allocType, memoryType, pages, memory)
	return status
}

// UEFIFreePages frees pages allocated via AllocatePages
//
//go:nosplit
func UEFIFreePages(memory uint64, pages uint64) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}

	// RISC-V assembly reads BootServices->FreePages from global systemTable
	status := uefiFreePages(memory, pages)
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

	// Allocate below 4GB to stay within the linear map
	memory = linearMapMaxPA - 1
	allocType = AllocateMaxAddress
	status = UEFIAllocatePages(allocType, EfiLoaderData, pages, &memory)

	if status != EFI_SUCCESS {
		return 0, false
	}

	return uintptr(memory), true
}

// memMapBuf is a global buffer for GetMemoryMap (avoids blowing nosplit stack limit)
var memMapBuf [16384]byte

// UEFIExitBootServices calls ExitBootServices to terminate UEFI boot services.
// After this call, boot services memory may be reclaimed and no boot service
// functions may be called. GetMemoryMap must be called first to obtain the map key.
//
//go:nosplit
func UEFIExitBootServices() EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}

	var mapSize, mapKey, descSize, descVer uint64

	// Get the memory map to obtain the map key
	mapSize = uint64(len(memMapBuf))
	status := uefiGetMemoryMap(&mapSize, (*uint64)(unsafe.Pointer(&memMapBuf[0])), &mapKey, &descSize, &descVer)
	if status != EFI_SUCCESS {
		return status
	}

	// Call ExitBootServices with the map key
	status = uefiExitBootServices(uintptr(imageHandle), mapKey)
	if status != EFI_SUCCESS {
		// If it fails, the map key may have changed. Retry once.
		mapSize = uint64(len(memMapBuf))
		status = uefiGetMemoryMap(&mapSize, (*uint64)(unsafe.Pointer(&memMapBuf[0])), &mapKey, &descSize, &descVer)
		if status != EFI_SUCCESS {
			return status
		}
		status = uefiExitBootServices(uintptr(imageHandle), mapKey)
	}

	return status
}

// RISC-V UEFI call helpers - implemented in uefi_calls_riscv64.s
// These read function pointers from the global systemTable

//go:noescape
func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS

//go:noescape
func uefiFreePages(memory uint64, pages uint64) EFI_STATUS

//go:noescape
func uefiGetMemoryMap(memoryMapSize *uint64, memoryMap *uint64, mapKey, descriptorSize, descriptorVersion *uint64) EFI_STATUS

//go:noescape
func uefiExitBootServices(imageHandle uintptr, mapKey uint64) EFI_STATUS
