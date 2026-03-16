// diplomat/main/startup_env.go - Build argc/argv/envp/auxv on g0 stack
//
// Creates the Linux-style process startup environment that Go's runtime expects.
// All boot parameters are passed via auxv entries, eliminating StartupParams.

package main

import (
	"unsafe"
)

// BuildStartupEnv builds the argc/argv/envp/auxv structure on the g0 stack.
// Returns the stack pointer (pointing to argc) for setting SP_EL0.
//
// Memory layout on g0 stack (768 bytes below stack top):
//   [0]    argc = 1
//   [8]    argv[0] → "kmazarin" string
//   [16]   argv[1] = NULL
//   [24]   envp[0] → "GODEBUG=gctrace=1"
//   [32]   envp[1] → "GOMEMLIMIT=NNMiB" (from kernel_mem_limit config)
//   [40]   envp[2] → "GOGC=NNNNN" (from gc_percent_kernel config)
//   [48]   envp[3] = NULL
//   [56+]  auxv entries (key, value pairs) — up to 20 entries (320 bytes)
//   ...
//   [384]  "kmazarin\0"
//   [400]  16 random bytes
//   [416]  "GODEBUG=gctrace=1\0"
//   [464]  "GOMEMLIMIT=NNMiB\0"
//   [496]  "GOGC=NNNNN\0"
//
// GOGC is set from gc_percent_kernel in kmazarin.toml (default not set = Go default 100%).
// GOMEMLIMIT is set from kernel_mem_limit in kmazarin.toml (default 24MB).
// Userspace programs get GOGC and GOMEMLIMIT from the TOML config via launch.go.
func BuildStartupEnv(vm *KernelVM, hw *HardwareInfo, kernel *LoadedKernel, cfg *KmazarinConfig) (uint64, error) {
	const paramSize = 0x300 // 768 bytes — room for 20 auxv entries + strings

	// Place structure 512 bytes below g0 stack top
	structStart := vm.G0StackTopVA - paramSize

	// We need to write to the PHYSICAL address of the stack
	// The g0 stack physical base corresponds to KernelG0StackBottom VA
	physOffset := structStart - KernelG0StackBottom
	structPhys := vm.G0StackPhys + physOffset

	// Zero the structure area
	plat.ZeroMemory(structPhys, paramSize)

	// Get a uint64 array view of the physical memory
	data := (*[64]uint64)(unsafe.Pointer(uintptr(structPhys)))

	// Write strings at known offsets (physical addresses)
	// Strings start at offset 384, safely past the auxv area (max ~20 entries ending at ~368)
	// Program name "kmazarin" at offset 384
	progName := (*[16]byte)(unsafe.Pointer(uintptr(structPhys + 384)))
	progName[0] = 'k'
	progName[1] = 'm'
	progName[2] = 'a'
	progName[3] = 'z'
	progName[4] = 'a'
	progName[5] = 'r'
	progName[6] = 'i'
	progName[7] = 'n'
	progName[8] = 0

	// Random bytes at offset 400
	randomPhys := structPhys + 400
	getTimerBasedRandom(randomPhys)

	// "GODEBUG=gctrace=1" at offset 416
	godebug := (*[48]byte)(unsafe.Pointer(uintptr(structPhys + 416)))
	s := "GODEBUG=gctrace=1"
	for i := 0; i < len(s); i++ {
		godebug[i] = s[i]
	}
	godebug[len(s)] = 0

	// "GOMEMLIMIT=NNMiB" at offset 464
	memlimit := (*[24]byte)(unsafe.Pointer(uintptr(structPhys + 464)))
	// Build "GOMEMLIMIT=" prefix
	prefix := "GOMEMLIMIT="
	off := 0
	for off < len(prefix) {
		memlimit[off] = prefix[off]
		off++
	}
	// Convert KernelMemLimitMB to decimal digits
	kmLimit := uint64(24) // default
	if cfg != nil && cfg.KernelMemLimitMB > 0 {
		kmLimit = cfg.KernelMemLimitMB
	}
	// Write decimal digits (max 4 digits for MB value)
	var digits [4]byte
	dLen := 0
	tmp := kmLimit
	for tmp > 0 {
		dLen++
		tmp /= 10
	}
	if dLen == 0 {
		dLen = 1
		digits[0] = '0'
	} else {
		tmp = kmLimit
		for j := dLen - 1; j >= 0; j-- {
			digits[j] = byte('0' + tmp%10)
			tmp /= 10
		}
	}
	for j := 0; j < dLen; j++ {
		memlimit[off] = digits[j]
		off++
	}
	// Append "MiB\0"
	memlimit[off] = 'M'
	memlimit[off+1] = 'i'
	memlimit[off+2] = 'B'
	memlimit[off+3] = 0

	// "GOGC=NNNNN" at offset 496
	gogcBuf := (*[16]byte)(unsafe.Pointer(uintptr(structPhys + 496)))
	gogcPrefix := "GOGC="
	gOff := 0
	for gOff < len(gogcPrefix) {
		gogcBuf[gOff] = gogcPrefix[gOff]
		gOff++
	}
	gcKernel := uint64(0) // 0 means don't set (Go default 100%)
	if cfg != nil && cfg.GCPercentKernel > 0 {
		gcKernel = cfg.GCPercentKernel
	}
	hasGOGC := gcKernel > 0
	if hasGOGC {
		var gcDigits [6]byte
		gcDLen := 0
		tmp = gcKernel
		for tmp > 0 {
			gcDLen++
			tmp /= 10
		}
		if gcDLen == 0 {
			gcDLen = 1
			gcDigits[0] = '0'
		} else {
			tmp = gcKernel
			for j := gcDLen - 1; j >= 0; j-- {
				gcDigits[j] = byte('0' + tmp%10)
				tmp /= 10
			}
		}
		for j := 0; j < gcDLen; j++ {
			gogcBuf[gOff] = gcDigits[j]
			gOff++
		}
		gogcBuf[gOff] = 0
	}

	// Now fill the structure using VIRTUAL addresses for pointers
	// (kmazarin will access them via TTBR1 high-memory VAs)

	// argc = 1
	data[0] = 1
	// argv[0] = pointer to "kmazarin" string (VA)
	data[1] = structStart + 384
	// argv[1] = NULL
	data[2] = 0
	// envp[0] = "GODEBUG=gctrace=1" (VA)
	data[3] = structStart + 416
	// envp[1] = "GOMEMLIMIT=NNMiB" (VA)
	data[4] = structStart + 464

	envIdx := 5
	if hasGOGC {
		// envp[2] = "GOGC=NNNNN" (VA)
		data[envIdx] = structStart + 496
		envIdx++
	}
	// envp terminator = NULL
	data[envIdx] = 0
	envIdx++

	// Auxiliary vector (starts after envp NULL)
	i := envIdx

	// AT_PAGESZ = 6: Physical page size
	data[i] = 6 // AT_PAGESZ
	data[i+1] = 4096
	i += 2

	// AT_RANDOM = 25: pointer to 16 random bytes (VA)
	data[i] = 25 // AT_RANDOM
	data[i+1] = structStart + 400
	i += 2

	// AT_SECURE = 23: not secure
	data[i] = 23 // AT_SECURE
	data[i+1] = 0
	i += 2

	// AT_HWCAP = 16: CPU capabilities (dynamic from ID registers)
	data[i] = 16 // AT_HWCAP
	data[i+1] = computeHWCAP()
	i += 2

	// Custom AT_* entries

	// AT_KMAZARIN_HEAP_START/END
	data[i] = 0x1010 // AT_KMAZARIN_HEAP_START
	data[i+1] = KernelHeapStart
	i += 2
	data[i] = 0x1011 // AT_KMAZARIN_HEAP_END
	data[i+1] = KernelHeapEnd
	i += 2

	// AT_KMAZARIN_SIZE
	data[i] = 0x1003 // AT_KMAZARIN_SIZE
	data[i+1] = kernel.HighestVirt - kernel.LowestVirt
	i += 2

	// AT_FRAME_POOL_START/END (use heap pool as frame pool)
	data[i] = 0x1004 // AT_FRAME_POOL_START
	data[i+1] = vm.HeapPagePoolBase
	i += 2
	data[i] = 0x1005 // AT_FRAME_POOL_END
	data[i+1] = vm.HeapPagePoolEnd
	i += 2

	// AT_TTBR1_L0_PHYS (kernel page table root: TTBR1 L0 on ARM64, PML4 on x86)
	data[i] = 0x1008 // AT_TTBR1_L0_PHYS
	data[i+1] = kernelPageTablePhys(vm)
	i += 2

	// AT_TTBR0_L0_PHYS (current UEFI page table root: TTBR0 on ARM64, CR3 on x86)
	data[i] = 0x1013 // AT_TTBR0_L0_PHYS
	data[i+1] = readBootPageTableBase()
	i += 2

	// AT_CPU_COUNT
	data[i] = 0x100C // AT_CPU_COUNT
	data[i+1] = hw.CPUCount
	i += 2

	// AT_RAM_BASE
	data[i] = 0x100D // AT_RAM_BASE
	data[i+1] = hw.RAMBase
	i += 2

	// AT_RAM_SIZE
	data[i] = 0x100E // AT_RAM_SIZE
	data[i+1] = hw.RAMSize
	i += 2

	// AT_UNIFIED_POOL_START/END
	data[i] = 0x100F // AT_UNIFIED_POOL_START
	data[i+1] = vm.UnifiedPoolStart
	i += 2
	data[i] = 0x1012 // AT_UNIFIED_POOL_END
	data[i+1] = vm.UnifiedPoolEnd
	i += 2

	// AT_KERNEL_BUDGET_MB - kernel memory warning threshold (from kmazarin.toml)
	if cfg != nil && cfg.KernelBudgetMB > 0 {
		data[i] = 0x1014 // AT_KERNEL_BUDGET_MB
		data[i+1] = cfg.KernelBudgetMB
		i += 2
	}

	// AT_DTB_PHYS - Device Tree Blob physical address
	// Try UEFI config table first, fall back to synthesized DTB
	dtbAddr := findDTBFromUEFI()
	if dtbAddr == 0 {
		dtbAddr = getOrBuildDTB(hw)
	}
	if dtbAddr != 0 {
		data[i] = 0x1000 // AT_DTB_PHYS
		data[i+1] = dtbAddr
		i += 2
	}

	// AT_NULL terminator
	data[i] = 0 // AT_NULL
	data[i+1] = 0

	printString("Startup env at VA ")
	printHex(structStart)
	printString(" (phys ")
	printHex(structPhys)
	printString(")\r\n")

	return structStart, nil
}

// findDTBFromUEFI searches the UEFI configuration table for the FDT (DTB).
// Returns the physical address of the DTB, or 0 if not found.
//
// EFI_DTB_TABLE_GUID = {B1B621D5-F19C-41A5-830B-D9152C69AAE0}
func findDTBFromUEFI() uint64 {
	st := systemTable
	if st == nil {
		return 0
	}
	numEntries := int(st.NumberOfTableEntries)
	tablePtr := st.ConfigurationTable
	if tablePtr == 0 || numEntries == 0 {
		return 0
	}

	// Each EFI_CONFIGURATION_TABLE entry: 16 bytes GUID + 8 bytes pointer = 24 bytes
	const entrySize = 24
	// EFI_DTB_TABLE_GUID bytes in memory (little-endian for Data1/Data2/Data3):
	// Data1=0xB1B621D5 Data2=0xF19C Data3=0x41A5 Data4={83,0B,D9,15,2C,69,AA,E0}
	var dtbGUID [16]byte
	// Data1 (uint32 LE): D5 21 B6 B1
	dtbGUID[0] = 0xD5
	dtbGUID[1] = 0x21
	dtbGUID[2] = 0xB6
	dtbGUID[3] = 0xB1
	// Data2 (uint16 LE): 9C F1
	dtbGUID[4] = 0x9C
	dtbGUID[5] = 0xF1
	// Data3 (uint16 LE): A5 41
	dtbGUID[6] = 0xA5
	dtbGUID[7] = 0x41
	// Data4 (8 bytes, raw): 83 0B D9 15 2C 69 AA E0
	dtbGUID[8] = 0x83
	dtbGUID[9] = 0x0B
	dtbGUID[10] = 0xD9
	dtbGUID[11] = 0x15
	dtbGUID[12] = 0x2C
	dtbGUID[13] = 0x69
	dtbGUID[14] = 0xAA
	dtbGUID[15] = 0xE0

	for i := 0; i < numEntries; i++ {
		entryAddr := tablePtr + uintptr(i*entrySize)
		guid := (*[16]byte)(unsafe.Pointer(entryAddr))

		match := true
		for j := 0; j < 16; j++ {
			if guid[j] != dtbGUID[j] {
				match = false
				break
			}
		}
		if match {
			// Vendor table pointer is at offset 16
			vendorTable := *(*uint64)(unsafe.Pointer(entryAddr + 16))
			return vendorTable
		}
	}
	return 0
}

// getTimerBasedRandom fills a memory region with pseudorandom bytes
// derived from a platform timer (ARM64: CNTVCT, x86_64: RDTSC).
func getTimerBasedRandom(physAddr uint64) {
	ptr := (*[16]byte)(unsafe.Pointer(uintptr(physAddr)))
	val := readTimerCounter()
	// Mix the timer value into 16 bytes
	for i := 0; i < 16; i++ {
		val = val*6364136223846793005 + 1442695040888963407 // LCG
		ptr[i] = byte(val >> 32)
	}
}

// readTimerCounter reads a platform counter for pseudorandom seed.
// ARM64: CNTVCT_EL0 (in pagetable_arm64.s)
// x86_64: RDTSC (in uefi_calls_amd64.s)
func readTimerCounter() uint64

// readBootPageTableBase returns the physical address of the current
// UEFI page table root, masked to the address field.
// ARM64: TTBR0_EL1 & mask (in pagetable_arm64.go)
// x86_64: CR3 & mask (in pagetable_amd64.go)
// NOTE: This is a Go function, not assembly — declared in arch-specific .go files.

// kernelPageTablePhys returns the kernel page table root physical address
// from the KernelVM struct.
// ARM64: vm.TTBR1L0Phys, x86_64: vm.PML4Phys
// Implemented in arch-specific files.
