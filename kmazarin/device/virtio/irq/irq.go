// Package irq implements shared MSI-X / interrupt programming code for VirtIO
// PCI drivers (input, block, GPU, net). It is intentionally driver-neutral:
// it imports only the PCI transport and low-level kernel primitives, never a
// specific device package, so any VirtIO driver can configure MSI-X without
// creating an import cycle.
package irq

import (
	"mazzy/kmazarin/pci"
)

// QEMU ARM virt machine maps PCI INTx to GIC SPI 3-6.
// The swizzle formula: spi = (slot + pin - 1) % 4 + 3
// GIC IRQ ID = SPI number + 32
const (
	PCIE_SPI_BASE  = 3  // First SPI number for PCI INTx
	GIC_SPI_OFFSET = 32 // SPI 0 = GIC IRQ 32
)

// GICv2m MSI controller on QEMU ARM virt
const (
	GICV2M_PHYS   = 0x08020000 // GICv2m physical base
	GICV2M_SIZE   = 0x1000     // 4KB
	GICV2M_TYPER  = 0x008      // MSI type register offset
	GICV2M_SETSPI = 0x040      // MSI doorbell register offset
)

// gicv2mBase holds the kernel virtual address of the GICv2m after mapping.
var gicv2mBase uintptr

// gicv2mInitDone tracks whether initGICv2mSPIBase has been called.
var gicv2mInitDone bool

// MSI-X table entry structure (16 bytes per entry)
const (
	MSIX_ENTRY_ADDR_LO = 0x00 // Message address low 32 bits
	MSIX_ENTRY_ADDR_HI = 0x04 // Message address high 32 bits
	MSIX_ENTRY_DATA    = 0x08 // Message data (SPI number)
	MSIX_ENTRY_CONTROL = 0x0C // Vector control (bit 0 = mask)
	MSIX_ENTRY_SIZE    = 16
)

// PCI MSI-X capability offsets
const (
	PCI_CAP_MSIX        = 0x11
	MSIX_MSGCTRL_OFFSET = 2 // Message control at cap+2
	MSIX_TABLE_OFFSET   = 4 // Table offset/BIR at cap+4
	MSIX_PBA_OFFSET     = 8 // PBA offset/BIR at cap+8
)

// DeviceMSIXVector is the MSI-X vector that VirtIO drivers assign to the device
// config register and every virtqueue during the handshake. Both arches use
// vector 0 — the arch-specific work (LAPIC target on amd64 vs GICv2m doorbell on
// arm64) lives in ConfigureMSIXForDevice; per-vector routing is unnecessary.
//
// Used by VirtIO devices that are MSI-X on both arches (block, net). Input
// deliberately uses INTx on ARM64 (returns virtio.MSIXNoVector from its own
// platformMSIXVector) and does not reference this const.
const DeviceMSIXVector uint16 = 0

// nextMSIXSPI tracks the next available GICv2m SPI for MSI-X allocation
var nextMSIXSPI uint32

// nextMSIXBarAddr tracks the next PCI MMIO address for unassigned MSI-X table BARs.
// Uses PCI_MMIO_BASE+0x80000 to avoid conflicts with common/notify BARs.
var nextMSIXBarAddr = uint32(pci.PCI_MMIO_BASE + 0x80000)

// allocateMSIXSPI allocates the next available GICv2m SPI number.
func allocateMSIXSPI() uint32 {
	spi := nextMSIXSPI
	nextMSIXSPI++
	return spi
}

// AllocateMSIXSPI is the public version of allocateMSIXSPI.
// Used by other VirtIO drivers (block, GPU) that configure their own MSI-X.
func AllocateMSIXSPI() uint32 {
	return allocateMSIXSPI()
}
