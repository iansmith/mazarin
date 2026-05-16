//go:build arm64

package irq

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
)

// initGICv2mSPIBase maps the GICv2m and reads TYPER to determine the SPI base.
// Must be called once before configureMSIX. Idempotent.
//
//go:nosplit
func initGICv2mSPIBase() {
	if gicv2mInitDone {
		return
	}
	gicv2mInitDone = true
	if err := kmem.MapDeviceMMIO(GICV2M_PHYS, GICV2M_SIZE); err != nil {
		klog.Errf("[MSI-X] ERROR: Failed to map GICv2m: %v\n", err)
		return
	}
	gicv2mBase = 0xFFFFFFFF00000000 + GICV2M_PHYS
	typer := asm.MmioRead32(gicv2mBase + GICV2M_TYPER)
	spiBase := (typer >> 16) & 0x3FF
	_ = typer & 0x3FF // spiCount — used only for validation
	nextMSIXSPI = spiBase
}

// configureMSIX finds the MSI-X capability, programs MSI-X table entries
// to target the GICv2m doorbell, and enables MSI-X.
// Returns the GIC IRQ number (SPI + 32) for this device, or 0 on failure.
//
//go:nosplit
func configureMSIX(bus, slot, funcNum uint8) uint32 {
	// Find MSI-X capability
	capPtr := pci.ConfigRead8(bus, slot, funcNum, pci.PCI_CAPABILITIES)
	var msixCapPtr uint8
	for i := 0; i < 32 && capPtr != 0 && capPtr != 0xFF; i++ {
		capID := pci.ConfigRead8(bus, slot, funcNum, capPtr)
		if capID == PCI_CAP_MSIX {
			msixCapPtr = capPtr
			break
		}
		capPtr = pci.ConfigRead8(bus, slot, funcNum, capPtr+1)
	}
	if msixCapPtr == 0 {
		klog.Errf("[MSI-X] ERROR: No MSI-X capability found\n")
		return 0
	}

	// Read message control to get table size
	msgCtrlWord := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr)
	msgCtrl := uint16(msgCtrlWord >> 16)
	tableSize := (msgCtrl & 0x7FF) + 1 // Bits [10:0] = table size - 1

	// Read table BAR and offset
	tableOffsetBIR := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr+MSIX_TABLE_OFFSET)
	tableBIR := tableOffsetBIR & 0x7             // BAR index
	tableOffset := tableOffsetBIR & ^uint32(0x7) // Offset within BAR

	// Always reprogram the MSI-X table BAR to a unique address from our pool.
	// UEFI-assigned addresses can collide with BARs reprogrammed by other drivers
	// (e.g., keyboard BAR4 reprogrammed to 0x10040000 overlaps mouse's UEFI BAR1 at 0x10041000).
	barOffset := uint8(0x10 + tableBIR*4)
	barAddr := pci.ConfigRead32(bus, slot, funcNum, barOffset)
	is64bit := barAddr&0x6 == 0x4

	pciAddr := nextMSIXBarAddr
	nextMSIXBarAddr += 0x1000
	pci.ConfigWrite32(bus, slot, funcNum, barOffset, pciAddr)
	if is64bit {
		pci.ConfigWrite32(bus, slot, funcNum, barOffset+4, 0) // Clear high 32 bits
	}
	barAddr = pci.ConfigRead32(bus, slot, funcNum, barOffset)
	barBasePA := uintptr(barAddr & 0xFFFFFFF0)

	// Map MSI-X BAR into TTBR1 kernel space
	kmem.MapDeviceMMIO(barBasePA, 0x1000)
	barBase := barBasePA + constants.KernelMMIOOffset

	tableBase := barBase + uintptr(tableOffset)

	// Allocate an SPI and program all vectors to the same SPI
	spi := allocateMSIXSPI()
	gicIRQ := spi + GIC_SPI_OFFSET

	// Enable MSI-X with function mask so we can safely program the table
	low16 := msgCtrlWord & 0xFFFF
	enableAndMask := uint32(msgCtrl) | (1 << 15) | (1 << 14) // enable + function mask
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableAndMask<<16))

	// Program all table entries to point to GICv2m doorbell.
	// MSI-X data must contain the SPI number (not the GIC IRQ number).
	// The GICv2m SETSPI register maps SPI N → GIC IRQ N+32 internally.
	doorbellAddr := uint64(GICV2M_PHYS + GICV2M_SETSPI)
	for i := uint32(0); i < uint32(tableSize); i++ {
		entryAddr := tableBase + uintptr(i*MSIX_ENTRY_SIZE)
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_LO, uint32(doorbellAddr))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_HI, uint32(doorbellAddr>>32))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_DATA, gicIRQ) // GICv2m expects SPI+32
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_CONTROL, 0)   // Unmask
	}
	asm.Dsb()

	// Readback MSI-X table entry 0 to verify programming
	readData := asm.MmioRead32(tableBase + MSIX_ENTRY_DATA)
	readCtrl := asm.MmioRead32(tableBase + MSIX_ENTRY_CONTROL)
	if readData != gicIRQ || readCtrl != 0 {
		klog.Errf("[MSI-X] MSI-X entry[0] mismatch: data=%d (expect %d) ctrl=0x%x\n",
			readData, gicIRQ, readCtrl)
	}

	// Clear function mask (keep enable)
	enableOnly := (uint32(msgCtrl) | (1 << 15)) &^ (1 << 14) // enable=1, function mask=0
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableOnly<<16))

	// Verify MSI-X is actually enabled in PCI config
	finalMsgCtrl := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr)
	finalCtrl := uint16(finalMsgCtrl >> 16)
	msixEnabled := (finalCtrl & (1 << 15)) != 0
	funcMasked := (finalCtrl & (1 << 14)) != 0
	readAddr := asm.MmioRead32(tableBase + MSIX_ENTRY_ADDR_LO)
	readDataFinal := asm.MmioRead32(tableBase + MSIX_ENTRY_DATA)
	_ = msixEnabled
	_ = funcMasked
	_ = readAddr
	_ = readDataFinal
	return gicIRQ
}

// ConfigureMSIXForDevice configures MSI-X for an external VirtIO device
// (block, GPU) on ARM64 via GICv2m. Programs the MSI-X table to target the
// GICv2m SETSPI doorbell and allocates a unique SPI for this device.
// Returns the GIC IRQ number (SPI + 32) for NonTimerIRQTopHalf, or 0 on failure.
//
//go:nosplit
func ConfigureMSIXForDevice(bus, slot, funcNum uint8) uint32 {
	initGICv2mSPIBase()
	return configureMSIX(bus, slot, funcNum)
}
