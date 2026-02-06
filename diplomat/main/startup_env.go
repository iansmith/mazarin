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
//   [24]   envp[0] → "GOGC=off"
//   [32]   envp[1] → "GODEBUG=asyncpreemptoff=1"
//   [40]   envp[2] = NULL
//   [48+]  auxv entries (key, value pairs) — up to 20 entries (320 bytes)
//   ...
//   [384]  "kmazarin\0"
//   [400]  16 random bytes
//   [416]  "GOGC=off\0"
//   [432]  "GODEBUG=asyncpreemptoff=1\0"
func BuildStartupEnv(vm *KernelVM, hw *HardwareInfo, kernel *LoadedKernel) (uint64, error) {
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

	// "GOGC=off" at offset 416
	gogc := (*[16]byte)(unsafe.Pointer(uintptr(structPhys + 416)))
	gogc[0] = 'G'
	gogc[1] = 'O'
	gogc[2] = 'G'
	gogc[3] = 'C'
	gogc[4] = '='
	gogc[5] = 'o'
	gogc[6] = 'f'
	gogc[7] = 'f'
	gogc[8] = 0

	// "GODEBUG=asyncpreemptoff=1" at offset 432
	godebug := (*[32]byte)(unsafe.Pointer(uintptr(structPhys + 432)))
	s := "GODEBUG=asyncpreemptoff=1"
	for i := 0; i < len(s); i++ {
		godebug[i] = s[i]
	}
	godebug[len(s)] = 0

	// Now fill the structure using VIRTUAL addresses for pointers
	// (kmazarin will access them via TTBR1 high-memory VAs)

	// argc = 1
	data[0] = 1
	// argv[0] = pointer to "kmazarin" string (VA)
	data[1] = structStart + 384
	// argv[1] = NULL
	data[2] = 0
	// envp[0] = "GOGC=off" (VA)
	data[3] = structStart + 416
	// envp[1] = "GODEBUG=asyncpreemptoff=1" (VA)
	data[4] = structStart + 432
	// envp[2] = NULL
	data[5] = 0

	// Auxiliary vector (starts at index 6 = byte offset 48)
	i := 6

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

	// AT_HWCAP = 16: ASIMD capability
	data[i] = 16 // AT_HWCAP
	data[i+1] = 0x2 // HWCAP_ASIMD
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

	// AT_TTBR1_L0_PHYS
	data[i] = 0x1008 // AT_TTBR1_L0_PHYS
	data[i+1] = vm.TTBR1L0Phys
	i += 2

	// AT_TTBR0_L0_PHYS (current UEFI TTBR0)
	data[i] = 0x1013 // AT_TTBR0_L0_PHYS
	data[i+1] = readTTBR0() & DESC_ADDR_MASK
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

	// AT_DTB_PHYS - Device Tree Blob physical address from UEFI config table
	dtbAddr := findDTBFromUEFI()
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
// derived from the ARM generic timer.
func getTimerBasedRandom(physAddr uint64) {
	ptr := (*[16]byte)(unsafe.Pointer(uintptr(physAddr)))
	val := readCNTVCT()
	// Mix the timer value into 16 bytes
	for i := 0; i < 16; i++ {
		val = val*6364136223846793005 + 1442695040888963407 // LCG
		ptr[i] = byte(val >> 32)
	}
}

// readCNTVCT reads the virtual counter register
func readCNTVCT() uint64
