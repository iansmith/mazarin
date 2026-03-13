//go:build arm64

// diplomat/main/uefi_mem_arm64.go - UEFI memory allocation wrappers (ARM64)
//
// Provides Go wrappers around UEFI Boot Services memory functions.
// Used by mmap to allocate actual physical memory.
//
// NOTE: This file is ARM64-specific because the assembly helpers
// read function pointers from the global systemTable directly,
// rather than taking them as explicit parameters like x86_64.

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

	// ARM64 assembly reads BootServices->AllocatePages from global systemTable
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

	// ARM64 assembly reads BootServices->FreePages from global systemTable
	status := uefiFreePages(memory, pages)
	return status
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

// ARM64 UEFI call helpers - implemented in uefi_calls_arm64.s
// These read function pointers from the global systemTable

//go:noescape
func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS

//go:noescape
func uefiFreePages(memory uint64, pages uint64) EFI_STATUS

//go:noescape
func uefiGetMemoryMap(memoryMapSize *uint64, memoryMap *uint64, mapKey, descriptorSize, descriptorVersion *uint64) EFI_STATUS

//go:noescape
func uefiExitBootServices(imageHandle uintptr, mapKey uint64) EFI_STATUS
