package input

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
)

// platformInitInterrupts is a no-op on x86_64.
func platformInitInterrupts() {}

// nextAPICVector tracks the next LAPIC vector for MSI-X allocation.
// Vectors 32-47 are used for IOAPIC/device interrupts (matching isrDev32-47
// stubs in exceptions_amd64.s). We start at 42 to avoid conflicts with
// any low-numbered IOAPIC entries the APIC driver may use.
var nextAPICVector uint32 = 42

// platformConfigureDeviceIRQ configures MSI-X for a VirtIO device on x86_64.
//
// On x86, MSI-X writes directly to the LAPIC (0xFEE00000) with a vector number
// in the data field. This bypasses the IOAPIC entirely. The CPU receives the
// vector as if it came from the IOAPIC, and our isrDev32-47 stubs handle it.
//
// Returns the IRQ number (vector - 32) for use by NonTimerIRQTopHalf, or 0 on failure.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
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
		return 0 // No MSI-X capability
	}

	// Read message control to get table size
	msgCtrlWord := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr)
	msgCtrl := uint16(msgCtrlWord >> 16)
	tableSize := (msgCtrl & 0x7FF) + 1

	// Read table BAR and offset
	tableOffsetBIR := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr+MSIX_TABLE_OFFSET)
	tableBIR := tableOffsetBIR & 0x7
	tableOffset := tableOffsetBIR & ^uint32(0x7)

	// Use the UEFI-assigned BAR address (don't reprogram on x86, UEFI sets up
	// valid non-colliding addresses unlike ARM64 where reprogramming was needed)
	barOffset := uint8(0x10 + tableBIR*4)
	barAddr := pci.ConfigRead32(bus, slot, funcNum, barOffset)
	is64bit := barAddr&0x6 == 0x4
	barBasePA := uintptr(barAddr & 0xFFFFFFF0)
	if is64bit {
		hiAddr := pci.ConfigRead32(bus, slot, funcNum, barOffset+4)
		barBasePA |= uintptr(hiAddr) << 32
	}

	if barBasePA == 0 {
		return 0 // BAR not assigned
	}

	// Map MSI-X BAR into kernel space
	kmem.MapDeviceMMIO(barBasePA, 0x1000)
	barBase := barBasePA + constants.KernelMMIOOffset
	tableBase := barBase + uintptr(tableOffset)

	// Allocate a vector in the 32-47 range
	vector := nextAPICVector
	if vector >= 48 {
		return 0 // Exhausted device vector range
	}
	nextAPICVector++
	irqNum := vector - 32 // IOAPIC input number convention

	// Enable MSI-X with function mask so we can safely program the table
	low16 := msgCtrlWord & 0xFFFF
	enableAndMask := uint32(msgCtrl) | (1 << 15) | (1 << 14)
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableAndMask<<16))

	// Program all MSI-X table entries to target the LAPIC.
	// x86 MSI address format: 0xFEE00000 | (destID << 12)
	// x86 MSI data format: bits [7:0] = vector number
	lapicAddr := uint64(0xFEE00000) // Destination = LAPIC ID 0 (CPU 0)
	for i := uint32(0); i < uint32(tableSize); i++ {
		entryAddr := tableBase + uintptr(i*MSIX_ENTRY_SIZE)
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_LO, uint32(lapicAddr))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_HI, uint32(lapicAddr>>32))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_DATA, vector) // LAPIC vector
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_CONTROL, 0)   // Unmask
	}
	asm.Dsb()

	// Clear function mask (keep enable)
	enableOnly := (uint32(msgCtrl) | (1 << 15)) &^ (1 << 14)
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableOnly<<16))

	return irqNum
}

// ConfigureMSIXForDevice is a public wrapper around platformConfigureDeviceIRQ
// so other VirtIO drivers (block, GPU) can reuse the MSI-X setup.
// Returns the IRQ number (vector - 32) for this device, or 0 on failure.
func ConfigureMSIXForDevice(bus, slot, funcNum uint8) uint32 {
	return platformConfigureDeviceIRQ(bus, slot, funcNum)
}
