// diplomat/main/uefi_mem_amd64.go - UEFI memory allocation wrappers (x86_64)
//
// Provides Go wrappers around UEFI Boot Services memory functions.
// Used by mmap to allocate actual physical memory.
//
// NOTE: This file is x86_64-specific because the assembly helpers
// (uefiCall*) take function pointers as explicit parameters.
// ARM64 uses a different pattern in uefi_mem_arm64.go.

package main

import "unsafe"

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

	bs := systemTable.BootServices

	var mapSize, mapKey, descSize, descVer uint64

	// Get the memory map to obtain the map key
	mapSize = uint64(len(memMapBuf))
	status := uefiCallGetMemoryMap(bs.GetMemoryMap,
		&mapSize, (*uint64)(unsafe.Pointer(&memMapBuf[0])), &mapKey, &descSize, &descVer)
	if status != EFI_SUCCESS {
		return status
	}

	// Call ExitBootServices with the map key
	status = uefiCallExitBootServices(bs.ExitBootServices,
		uintptr(imageHandle), mapKey)
	if status != EFI_SUCCESS {
		// If it fails, the map key may have changed. Retry once.
		mapSize = uint64(len(memMapBuf))
		status = uefiCallGetMemoryMap(bs.GetMemoryMap,
			&mapSize, (*uint64)(unsafe.Pointer(&memMapBuf[0])), &mapKey, &descSize, &descVer)
		if status != EFI_SUCCESS {
			return status
		}
		status = uefiCallExitBootServices(bs.ExitBootServices,
			uintptr(imageHandle), mapKey)
	}

	return status
}

//go:noescape
func uefiCallGetMemoryMap(funcPtr uintptr, mapSize *uint64, mapBuf *uint64, mapKey, descSize, descVer *uint64) EFI_STATUS

//go:noescape
func uefiCallExitBootServices(funcPtr uintptr, imageHandle uintptr, mapKey uint64) EFI_STATUS

// uefiCallAllocatePages is implemented in assembly (uefi_calls.s)
// Calls UEFI AllocatePages using MS x64 calling convention
//
//go:noescape
func uefiCallAllocatePages(funcPtr uintptr, allocType uint32, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS

// uefiCallFreePages is implemented in assembly (uefi_calls.s)
// Calls UEFI FreePages using MS x64 calling convention
//
//go:noescape
func uefiCallFreePages(funcPtr uintptr, memory uint64, pages uint64) EFI_STATUS
