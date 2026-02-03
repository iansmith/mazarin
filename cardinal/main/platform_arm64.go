package main

import (
	"mazzy/cardinal/asm"
	"mazzy/shared/blockdev"
	"mazzy/shared/bootloader"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// Shared type aliases for use throughout cardinal
type LoadedKernel = bootloader.LoadedKernel

// Active function pointer tables
var plat bootloader.PlatformOps

// initDefaultPlatform populates the function pointer tables with
// cardinal's ARM64 bare-metal implementations.
func initDefaultPlatform() {
	plat = bootloader.PlatformOps{
		PrintChar: uartPutc,
		DebugOut:  uartPutc,

		AllocPhysPages: cardinalAllocPhysPages,
		FreePhysPages:  cardinalFreePhysPages,
		ZeroMemory:     cardinalZeroMemory,

		VM: bootloader.VMOps{
			MapPage:             cardinalMapPage,
			UnmapPage:           cardinalUnmapPage,
			MapRegion:           cardinalMapRegion,
			AllocAndMap:         cardinalAllocAndMap,
			FlushTLB:            cardinalFlushTLB,
			ReadPTRoot:          func() uint64 { return asm.ReadTtbr1El1() },
			WritePTRoot:         func(val uint64) { asm.WriteTtbr1El1(val) },
			DisableWriteProtect: func() {}, // ARM64: no-op
			EnableWriteProtect:  func() {}, // ARM64: no-op
		},

		ExitBootServices: func() error { return nil },
		GetBlockDevice:   nil, // Set later after VirtIO init
	}

}

// Adapter functions that bridge cardinal's existing signatures to shared types.

// cardinalAllocPhysPages allocates contiguous physical pages.
// Cardinal's allocKFrame only allocates single pages, so we allocate one at a time.
func cardinalAllocPhysPages(numPages uint64) (uint64, error) {
	if numPages == 0 {
		return 0, nil
	}
	// For the first page, get the base address
	first := allocKFrame()
	if first == 0 {
		return 0, &bootloader.ELFError{Msg: "out of physical frames"}
	}
	// Allocate remaining pages (they should be contiguous from bump allocator)
	for i := uint64(1); i < numPages; i++ {
		frame := allocKFrame()
		if frame == 0 {
			return 0, &bootloader.ELFError{Msg: "out of physical frames"}
		}
	}
	return uint64(first), nil
}

func cardinalFreePhysPages(_ uint64, _ uint64) error {
	// Cardinal's frame allocator doesn't support freeing
	return nil
}

func cardinalZeroMemory(addr, size uint64) {
	bzeroSimple(unsafe.Pointer(uintptr(addr)), uint32(size))
}

func cardinalMapPage(virt, phys uint64, flags uint64) error {
	// Decode flags into cardinal's mapPage parameters
	attrs := uint64(PTE_ATTR_NORMAL)
	ap := uint64(PTE_AP_RW_EL1)
	exec := uint64(PTE_EXEC_NEVER)

	if flags&bootloader.PF_X != 0 {
		exec = PTE_EXEC_ALLOW
	}
	if flags&bootloader.PF_W == 0 {
		ap = PTE_AP_RO_EL1
	}

	mapKernelPage(uintptr(virt), uintptr(phys), attrs, ap, exec)
	return nil
}

func cardinalUnmapPage(virt uint64) error {
	unmapPage(uintptr(virt))
	return nil
}

func cardinalMapRegion(virtBase, physBase, size uint64, flags uint64) error {
	numPages := (size + 0xFFF) >> 12
	for i := uint64(0); i < numPages; i++ {
		virt := virtBase + i*0x1000
		phys := physBase + i*0x1000
		if err := cardinalMapPage(virt, phys, flags); err != nil {
			return err
		}
	}
	return nil
}

func cardinalAllocAndMap(virt uint64, flags uint64) (uint64, error) {
	phys := allocKFrame()
	if phys == 0 {
		return 0, &bootloader.ELFError{Msg: "out of physical frames"}
	}
	if err := cardinalMapPage(virt, uint64(phys), flags); err != nil {
		return 0, err
	}
	return uint64(phys), nil
}

func cardinalFlushTLB(virt uint64) {
	asm.InvalidateTlbVa(uintptr(virt))
}

// cardinalGetBlockDevice discovers and initializes the VirtIO block device.
func cardinalGetBlockDevice() (blockdev.BlockDevice, error) {
	// Scan DTB for VirtIO MMIO devices
	var devices [maxVirtIODevices]VirtIOMMIODevice
	count := getVirtIOMMIOFromDTB(&devices)

	if count == 0 {
		return nil, &cardinalBootError{"no VirtIO MMIO devices in DTB"}
	}

	// Probe each device looking for a block device (DeviceID == 2)
	for i := 0; i < count; i++ {
		base := devices[i].Base
		size := devices[i].Size

		// Ensure the MMIO region is identity-mapped
		numPages := (size + 0xFFF) >> 12
		for p := uintptr(0); p < numPages; p++ {
			addr := (base &^ 0xFFF) + p*0x1000
			mapPage(addr, addr, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		}
		asm.Dsb()
		asm.Isb()

		// Check device ID before full init
		// Try to init as block device
		dev, err := bootloader.InitVirtIOBlock(base)
		if err != nil {
			continue
		}
		return dev, nil
	}

	return nil, &cardinalBootError{"no VirtIO block device found"}
}

// cardinalMountFilesystem mounts a FAT32 filesystem using the bump allocator.
func cardinalMountFilesystem(dev blockdev.BlockDevice) (*fat32.FileSystem, error) {
	fs := cNew[fat32.FileSystem]()
	if fs == nil {
		return nil, &cardinalBootError{"alloc failed"}
	}
	err := fat32.MountInto(fs, dev, cMallocAllocator)
	if err != nil {
		return nil, err
	}
	return fs, nil
}

// cardinalPrepareKernel creates the linear map and populates RuntimeConfig.
func cardinalPrepareKernel(kernel *LoadedKernel) error {
	// Create linear map for the unified pool region using 2MB block descriptors.
	// This ensures any PA from the buddy allocator has a valid kernel VA.
	// Must happen after kmazarin is loaded (its pages are already mapped with 4KB granularity).
	physAlloc := getKFrameAllocator()
	unifiedPoolStart := physAlloc.next &^ 0x1FFFFF // Align DOWN to 2MB
	ramEnd := uintptr(detectedRAMBase + detectedRAMSize)
	createLinearMap(unifiedPoolStart, ramEnd)

	// Get kmazarin's StartupParams address from linker symbol
	kmazarinStartupParamsVA := uintptr(LinkerKmazarinStartupParams)

	// Populate RuntimeConfig
	populateRuntimeConfig(kmazarinStartupParamsVA, kernelPageTableL0)

	return nil
}

// cardinalJumpToKernel sets up the startup environment and jumps to kmazarin.
func cardinalJumpToKernel(kernel *LoadedKernel) {
	kmazarinStartupParamsVA := uintptr(LinkerKmazarinStartupParams)

	// Set up argc/argv/envp/auxv structure
	stackPointer, argc, argv := setupKmazarinStartupEnv(kmazarinStartupParamsVA)
	if stackPointer == 0 || argv == 0 {
		uartPutsDirect("ERROR: Environment setup failed!\r\n")
		return
	}

	// Disable timer and IRQs before jumping
	timer_write_ctl(0)
	gicDisableInterrupt(timer_irq_id())
	asm.DisableIrqs()

	jumpToKmazarin(uintptr(kernel.Entry), argc, argv, stackPointer)

	// Should never reach here
	uartPutsDirect("ERROR: Returned from kmazarin!\r\n")
}

// cardinalBoot runs the boot sequence using function pointer tables.
// Called from kernelMainInternal after hardware init and MMU setup.
func cardinalBoot() {
	initDefaultPlatform()
	// 1. Discover block device
	dev, err := cardinalGetBlockDevice()
	if err != nil {
		cardinalFatal("block device discovery failed")
	}

	// 2. Test block read
	blkErr := dev.ReadBlock(0, cardinalTestBuf[:])
	if blkErr != nil {
		if blkErr == bootloader.ErrVirtIOTimeout {
			cardinalFatal("block read timeout (DMA issue?)")
		} else if blkErr == bootloader.ErrVirtIOReadFail {
			cardinalFatal("block read status error")
		} else {
			cardinalFatal("block read unknown error")
		}
	}

	// Mount FAT32 filesystem
	fsys, err := cardinalMountFilesystem(dev)
	if err != nil {
		cardinalFatal("FAT32 mount failed")
	}

	// 3. Load kmazarin kernel from disk
	kernel, err := cardinalLoadKernel(fsys, "/KMAZARIN.ELF")
	if err != nil {
		cardinalFatal("kernel load failed")
	}

	// 4. Prepare kernel (RuntimeConfig, etc.)
	if err := cardinalPrepareKernel(kernel); err != nil {
		cardinalFatal("kernel prepare failed")
	}

	// 5. Jump to kernel
	cardinalJumpToKernel(kernel)
}

// Global test buffer for VirtIO testing (avoids stack allocation)
var cardinalTestBuf [512]byte

// cardinalFatal prints a fatal error and halts.
func cardinalFatal(msg string) {
	uartPutsDirect("FATAL: ")
	uartPutsDirect(msg)
	uartPutsDirect("\r\n")
	asm.SemihostingExit()
	for {
	}
}
