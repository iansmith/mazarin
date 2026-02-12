//go:build riscv64

// diplomat/main/hardware_riscv64.go - RISC-V 64-bit UEFI hardware discovery
//
// Queries CPU count via EFI_MP_SERVICES_PROTOCOL and RAM size via GetMemoryMap.

package main

import "unsafe"

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
		hw.RAMSize = uint64(config.MaxRAMMB) * 1024 * 1024
	}

	return hw, nil
}

// queryCPUCount uses EFI_MP_SERVICES_PROTOCOL to get CPU count
func queryCPUCount() uint64 {
	var mpProtocol uintptr

	// Locate MP services protocol
	status := uefiLocateProtocol(uintptr(unsafe.Pointer(&mpServicesGUID)), 0, uintptr(unsafe.Pointer(&mpProtocol)))
	if status != EFI_SUCCESS {
		// MP services not available - assume single CPU
		return 1
	}

	var numProcs, numEnabled uint64
	status = uefiMPGetNumberOfProcessors(mpProtocol, uintptr(unsafe.Pointer(&numProcs)), uintptr(unsafe.Pointer(&numEnabled)))
	if status != EFI_SUCCESS {
		return 1
	}

	if numProcs == 0 {
		return 1
	}

	return numProcs
}

// queryRAM gets total RAM size from UEFI memory map
func queryRAM(hw *HardwareInfo) {
	var mapSize, mapKey, descSize, descVersion uint64
	mapSize = 4096 // Initial buffer size

	// Allocate buffer for memory map
	mapBuf := dMakeSlice[byte](int(mapSize))
	if mapBuf == nil {
		hw.RAMSize = 0
		return
	}

	// Get memory map
	status := uefiGetMemoryMap(
		&mapSize,
		(*uint64)(unsafe.Pointer(&mapBuf[0])),
		&mapKey,
		&descSize,
		&descVersion,
	)

	if status != EFI_SUCCESS {
		hw.RAMSize = 0
		return
	}

	// Sum up all usable memory regions
	var totalRAM uint64
	for offset := uint64(0); offset < mapSize; offset += descSize {
		desc := (*efiMemoryDescriptor)(unsafe.Pointer(&mapBuf[offset]))

		// Count conventional memory, boot services, and runtime services as available RAM
		if desc.Type == EfiConventionalMemory ||
			desc.Type == EfiBootServicesCode ||
			desc.Type == EfiBootServicesData ||
			desc.Type == EfiRuntimeServicesCode ||
			desc.Type == EfiRuntimeServicesData {
			totalRAM += desc.NumberOfPages * 4096
		}
	}

	hw.RAMSize = totalRAM
}

// efiMemoryDescriptor for RISC-V 64-bit (40 bytes with default descriptor size)
type efiMemoryDescriptor struct {
	Type          uint32
	_             uint32 // padding
	PhysicalStart uint64
	VirtualStart  uint64
	NumberOfPages uint64
	Attribute     uint64
}

// QueryHardwareRISCV returns hardcoded hardware info for QEMU virt platform.
// Used for RISC-V direct boot to avoid UEFI allocations during early boot.
// Returns 1 CPU and 2GB RAM (matching QEMU -m 2G setting).
func QueryHardwareRISCV(config *KmazarinConfig) (*HardwareInfo, error) {
	// For RISC-V direct boot on QEMU virt, hardcode the hardware info
	// QEMU virt with -m 2G: 1 CPU, 2GB RAM @ 0x80000000
	hw := dNew[HardwareInfo]()
	if hw == nil {
		printString("ERROR: Failed to allocate HardwareInfo\r\n")
		for {}
	}

	hw.CPUCount = 1
	hw.RAMBase = 0x80000000  // QEMU virt RISC-V RAM base
	hw.RAMSize = 2048 * 1024 * 1024  // 2GB (matches QEMU -m 2G)

	// Validate against config (using same logic as QueryHardware)
	if hw.CPUCount < config.MinCPUs {
		printString("FATAL: Too few CPUs\r\n")
		for {}
	}
	if hw.CPUCount > config.MaxCPUs {
		hw.CPUCount = config.MaxCPUs
	}

	ramMB := hw.RAMSize / (1024 * 1024)
	if ramMB < config.MinRAMMB {
		printString("FATAL: Too little RAM\r\n")
		for {}
	}
	if ramMB > config.MaxRAMMB {
		hw.RAMSize = uint64(config.MaxRAMMB) * 1024 * 1024
	}

	return hw, nil
}

// Pre-allocated errors
var (
	errHardwareAllocFailed = blockDevError{"hardware: allocation failed"}
	errTooFewCPUs          = blockDevError{"hardware: too few CPUs"}
	errTooLittleRAM        = blockDevError{"hardware: too little RAM"}
)
