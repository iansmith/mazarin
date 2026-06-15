//go:build arm64

// diplomat/main/hardware_arm64.go - ARM64 UEFI hardware discovery
//
// Queries CPU count via EFI_MP_SERVICES_PROTOCOL and RAM size via GetMemoryMap.

package main

import (
	"unsafe"

	"mazzy/shared/bootmem"
)

// EFI_MP_SERVICES_PROTOCOL GUID: {3FDDA605-A76E-4F46-AD29-12F4531B3D08}
var mpServicesGUID = [16]byte{
	0x05, 0xA6, 0xDD, 0x3F, // Data1 (little-endian)
	0x6E, 0xA7,             // Data2
	0x46, 0x4F,             // Data3
	0xAD, 0x29, 0x12, 0xF4, 0x53, 0x1B, 0x3D, 0x08, // Data4
}

// QueryHardware discovers CPU count and RAM size from UEFI services.
// Validates against config limits and caps/aborts as needed.
func QueryHardware(config *KmazarinConfig) (*HardwareInfo, error) {
	hw := dNew[HardwareInfo]()
	if hw == nil {
		return nil, &errHardwareAllocFailed
	}

	// Query CPU count
	hw.CPUCount = queryCPUCount()

	// Query RAM from memory map
	queryRAM(hw)

	// Validate against config
	if hw.CPUCount < config.MinCPUs {
		printString("FATAL: Too few CPUs (")
		printHex(hw.CPUCount)
		printString(" < ")
		printHex(config.MinCPUs)
		printString(")\r\n")
		return nil, &errTooFewCPUs
	}
	if hw.CPUCount > config.MaxCPUs {
		hw.CPUCount = config.MaxCPUs
	}

	ramMB := hw.RAMSize / (1024 * 1024)
	if ramMB < config.MinRAMMB {
		printString("FATAL: Too little RAM (")
		printHex(ramMB)
		printString("MB < ")
		printHex(config.MinRAMMB)
		printString("MB)\r\n")
		return nil, &errTooLittleRAM
	}
	if ramMB > config.MaxRAMMB {
		hw.RAMSize = config.MaxRAMMB * 1024 * 1024
	}

	return hw, nil
}

// queryCPUCount tries to get CPU count via EFI_MP_SERVICES_PROTOCOL.
// Returns 1 if the protocol is not available.
func queryCPUCount() uint64 {
	if systemTable == nil || systemTable.BootServices == nil {
		return 1
	}

	var mpProtocol uintptr
	status := uefiLocateProtocol(
		uintptr(unsafe.Pointer(&mpServicesGUID[0])),
		0,
		uintptr(unsafe.Pointer(&mpProtocol)),
	)
	if status != EFI_SUCCESS || mpProtocol == 0 {
		printString("MP protocol not found, assuming 1 CPU\r\n")
		return 1
	}

	var numProcs, numEnabled uint64
	status = uefiMPGetNumberOfProcessors(
		mpProtocol,
		uintptr(unsafe.Pointer(&numProcs)),
		uintptr(unsafe.Pointer(&numEnabled)),
	)
	if status != EFI_SUCCESS {
		printString("MP GetNumberOfProcessors failed, assuming 1 CPU\r\n")
		return 1
	}

	if numProcs == 0 {
		return 1
	}
	return numProcs
}

// EFI_MEMORY_DESCRIPTOR for ARM64 (40 bytes with default descriptor size)
type efiMemoryDescriptor struct {
	Type          uint32
	_             uint32 // padding
	PhysicalStart uint64
	VirtualStart  uint64
	NumberOfPages uint64
	Attribute     uint64
}

// memMapQueryBuf is a separate buffer for hardware queries (memMapBuf is used by ExitBootServices)
var memMapQueryBuf [32768]byte

// queryRAM enumerates the UEFI memory map to find RAM base and total size.
func queryRAM(hw *HardwareInfo) {
	var mapSize, mapKey, descSize, descVer uint64
	mapSize = uint64(len(memMapQueryBuf))

	status := uefiGetMemoryMap(
		&mapSize,
		(*uint64)(unsafe.Pointer(&memMapQueryBuf[0])),
		&mapKey, &descSize, &descVer,
	)
	if status != EFI_SUCCESS {
		// Fallback: assume QEMU virt defaults
		hw.RAMBase = 0x40000000
		hw.RAMSize = 1024 * 1024 * 1024 // 1GB default
		printString("GetMemoryMap failed, using defaults\r\n")
		return
	}

	if descSize == 0 {
		hw.RAMBase = 0x40000000
		hw.RAMSize = 1024 * 1024 * 1024
		return
	}

	// Collect usable RAM regions, then take the largest contiguous run below the
	// linear-map cap. The ARM64 kernel linear map (createDiplomatLinearMap in
	// kernelvm_arm64.go) clamps mapped PA to linearMapMaxPA (4GB), so RAMSize must
	// be bounded by that same cap — otherwise it would overstate usable RAM beyond
	// what is actually mapped and the kernel would derive pools over unmapped
	// memory. This matches hardware_amd64.go. qemu virt reports contiguous RAM, so
	// today this also matches the old lowest..highest span. See shared/bootmem.
	numDescs := mapSize / descSize
	n := 0
	for i := uint64(0); i < numDescs && n < len(ramRegionScratch); i++ {
		desc := (*efiMemoryDescriptor)(unsafe.Pointer(uintptr(unsafe.Pointer(&memMapQueryBuf[0])) + uintptr(i*descSize)))

		switch desc.Type {
		case EfiConventionalMemory, EfiBootServicesCode, EfiBootServicesData, EfiLoaderCode, EfiLoaderData:
			ramRegionScratch[n] = bootmem.Region{
				Start: desc.PhysicalStart,
				End:   desc.PhysicalStart + desc.NumberOfPages*4096,
			}
			n++
		}
	}

	if base, size, ok := bootmem.LargestContiguousRAM(ramRegionScratch[:n], linearMapMaxPA); ok {
		hw.RAMBase = base
		hw.RAMSize = size
	} else {
		hw.RAMBase = 0x40000000
		hw.RAMSize = 1024 * 1024 * 1024
	}
}

// Assembly declarations for UEFI calls

//go:noescape
func uefiLocateProtocol(protocol, registration, iface uintptr) EFI_STATUS

//go:noescape
func uefiMPGetNumberOfProcessors(protocol, numProcs, numEnabled uintptr) EFI_STATUS

// Pre-allocated errors
var (
	errHardwareAllocFailed = blockDevError{"hardware: allocation failed"}
	errTooFewCPUs          = blockDevError{"hardware: too few CPUs"}
	errTooLittleRAM        = blockDevError{"hardware: too little RAM"}
)
