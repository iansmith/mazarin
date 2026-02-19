// Package apic implements the x86_64 Local APIC and I/O APIC interrupt
// controllers for the QEMU virt machine.
//
// The Local APIC handles per-CPU interrupt delivery and the timer.
// The I/O APIC routes external device interrupts to CPUs.
//
// On QEMU, the LAPIC is at 0xFEE00000 and the I/O APIC is at 0xFEC00000.
// These addresses are hardcoded since x86_64 QEMU uses ACPI (not DTB)
// for device discovery.
package apic

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
)

// Local APIC register offsets (memory-mapped at LAPIC base)
const (
	LAPIC_ID      = 0x020 // Local APIC ID
	LAPIC_VERSION = 0x030 // Version register
	LAPIC_TPR     = 0x080 // Task Priority Register
	LAPIC_EOI     = 0x0B0 // End of Interrupt (write 0)
	LAPIC_SVR     = 0x0F0 // Spurious Interrupt Vector Register
	LAPIC_ISR0    = 0x100 // In-Service Register (bits 0-31)
	LAPIC_ICR_LO  = 0x300 // Interrupt Command Register (low)
	LAPIC_ICR_HI  = 0x310 // Interrupt Command Register (high)
	LAPIC_LVT_TMR = 0x320 // LVT Timer Register
	LAPIC_LVT_L0  = 0x350 // LVT LINT0 Register
	LAPIC_LVT_L1  = 0x360 // LVT LINT1 Register
	LAPIC_LVT_ERR = 0x370 // LVT Error Register
	LAPIC_TMRINITCNT = 0x380 // Timer Initial Count
	LAPIC_TMRCURRCNT = 0x390 // Timer Current Count
	LAPIC_TMRDIV  = 0x3E0 // Timer Divide Configuration
)

// I/O APIC registers (indirect access via IOREGSEL/IOWIN)
const (
	IOAPIC_REGSEL = 0x00 // Register selector (index)
	IOAPIC_WIN    = 0x10 // Data window

	// Indirect register indices
	IOAPIC_ID     = 0x00 // I/O APIC ID
	IOAPIC_VER    = 0x01 // Version (bits [23:16] = max redir entry)
	IOAPIC_REDTBL = 0x10 // Redirection table base (entries at 0x10+2*n)
)

// QEMU virt machine hardcoded addresses
const (
	LAPICPhysBase  = 0xFEE00000
	IOAPICPhysBase = 0xFEC00000
	LAPICSize      = 0x1000  // 4KB
	IOAPICSize     = 0x1000  // 4KB

	// Kernel VA offset — add to physical address for kernel virtual address
	KernelMMIOOffset = 0xFFFFFFFF00000000
)

// LVT timer modes
const (
	LVT_MASKED   = 1 << 16 // Interrupt masked
	LVT_PERIODIC = 1 << 17 // Periodic mode (vs one-shot)
)

// APICDriver implements device.Discoverable for the x86_64 APIC.
// Since x86_64 uses ACPI (not DTB), Compatible returns a synthetic
// string and Probe always returns true.
type APICDriver struct{}

func (d *APICDriver) Compatible() []string {
	return []string{"x86,local-apic"}
}

func (d *APICDriver) Probe(_ *dtb.Node) bool {
	// x86_64 QEMU always has a Local APIC + I/O APIC
	return true
}

func (d *APICDriver) Init(_ *dtb.Node) (deviceapi.Closable, error) {
	// Map LAPIC and IOAPIC MMIO regions
	if err := kmem.MapDeviceMMIO(LAPICPhysBase, LAPICSize); err != nil {
		return nil, err
	}
	if err := kmem.MapDeviceMMIO(IOAPICPhysBase, IOAPICSize); err != nil {
		return nil, err
	}

	dev := &APIC{
		lapicBase:  LAPICPhysBase + KernelMMIOOffset,
		ioapicBase: IOAPICPhysBase + KernelMMIOOffset,
	}

	dev.initHardware()
	return dev, nil
}

// APIC implements InterruptController for x86_64.
type APIC struct {
	lapicBase  uintptr
	ioapicBase uintptr
	handlers   [256]func()
}

func (a *APIC) Name() string { return "x86-apic" }

func (a *APIC) Close() error {
	// Disable LAPIC by clearing the enable bit in SVR
	svr := a.readLAPIC(LAPIC_SVR)
	a.writeLAPIC(LAPIC_SVR, svr & ^uint32(1<<8))
	return nil
}

// RegisterHandler registers an interrupt handler for the given IRQ.
// IRQ numbers map to I/O APIC inputs (0-23 typically).
// The handler is registered both locally and with kirq for dispatch.
func (a *APIC) RegisterHandler(irq uint32, handler func()) {
	if irq < 256 {
		a.handlers[irq] = handler
		kirq.RegisterSimpleHandler(irq, handler)
	}
}

// EnableIRQ enables an I/O APIC redirection entry.
// Unmasks the interrupt and routes it to CPU 0.
//
//go:nosplit
func (a *APIC) EnableIRQ(irq uint32) {
	if irq > 23 {
		return
	}
	// Read current redirection entry
	lo := a.readIOAPIC(IOAPIC_REDTBL + irq*2)
	// Clear mask bit (bit 16)
	lo &^= (1 << 16)
	a.writeIOAPIC(IOAPIC_REDTBL+irq*2, lo)
}

// DisableIRQ masks an I/O APIC redirection entry.
//
//go:nosplit
func (a *APIC) DisableIRQ(irq uint32) {
	if irq > 23 {
		return
	}
	lo := a.readIOAPIC(IOAPIC_REDTBL + irq*2)
	lo |= (1 << 16) // Set mask bit
	a.writeIOAPIC(IOAPIC_REDTBL+irq*2, lo)
}

// SetIRQPriority is a no-op on x86.
// On the IOAPIC, priority is implicit in the vector number (higher vector class =
// higher priority). Vectors are assigned as 32+irq during initHardware and must
// remain in that range for the exception handler's dispatch logic (vectors 32-47
// route to handle_device_irq). Unlike ARM64's GIC, there is no separate priority
// register.
//
//go:nosplit
func (a *APIC) SetIRQPriority(irq uint32, priority uint8) {
	// no-op: vector = 32+irq was set by initHardware
}

// SetIRQTarget is a no-op on x86 single-core.
// All IOAPIC entries are routed to LAPIC ID 0 (CPU 0) during initHardware.
// The ARM64 GIC uses a bitmask (0x01 = CPU 0), but the IOAPIC uses a LAPIC ID
// in physical destination mode, so the ARM64 value (0x01) would incorrectly
// target CPU 1 instead of CPU 0.
//
//go:nosplit
func (a *APIC) SetIRQTarget(irq uint32, cpuMask uint8) {
	// no-op: destination = LAPIC ID 0 was set by initHardware
}

// SetIRQEdgeTriggered configures the IRQ as edge-triggered.
//
//go:nosplit
func (a *APIC) SetIRQEdgeTriggered(irq uint32) {
	if irq > 23 {
		return
	}
	lo := a.readIOAPIC(IOAPIC_REDTBL + irq*2)
	lo &^= (1 << 15) // Clear trigger mode bit (0 = edge)
	a.writeIOAPIC(IOAPIC_REDTBL+irq*2, lo)
}

// SetIRQLevelTriggered configures the IRQ as level-triggered, active-low.
// Required for PCI INTx interrupts routed through the IOAPIC.
//
//go:nosplit
func (a *APIC) SetIRQLevelTriggered(irq uint32) {
	if irq > 23 {
		return
	}
	lo := a.readIOAPIC(IOAPIC_REDTBL + irq*2)
	lo |= (1 << 15) // Set trigger mode bit (1 = level)
	lo |= (1 << 13) // Set polarity bit (1 = active-low)
	a.writeIOAPIC(IOAPIC_REDTBL+irq*2, lo)
}

// EOI signals end-of-interrupt to the Local APIC.
//
//go:nosplit
func (a *APIC) EOI() {
	a.writeLAPIC(LAPIC_EOI, 0)
}

// initHardware initializes the Local APIC and I/O APIC.
func (a *APIC) initHardware() {
	// === Local APIC initialization ===

	// Set Task Priority to 0 (accept all interrupts)
	a.writeLAPIC(LAPIC_TPR, 0)

	// Enable LAPIC with spurious vector 0xFF
	// Bit 8 = APIC Software Enable
	a.writeLAPIC(LAPIC_SVR, 0x1FF)

	// Mask all LVT entries initially
	a.writeLAPIC(LAPIC_LVT_TMR, LVT_MASKED)
	a.writeLAPIC(LAPIC_LVT_L0, LVT_MASKED)
	a.writeLAPIC(LAPIC_LVT_L1, LVT_MASKED)
	a.writeLAPIC(LAPIC_LVT_ERR, LVT_MASKED)

	// === I/O APIC initialization ===

	// Read max redirection entries from IOAPIC version register
	ver := a.readIOAPIC(IOAPIC_VER)
	maxRedir := (ver >> 16) & 0xFF

	// Mask all I/O APIC entries and set default vectors
	// Vector = 32 + irq (vectors 0-31 are reserved for CPU exceptions)
	for i := uint32(0); i <= maxRedir; i++ {
		// Low 32 bits: vector = 32+i, masked, physical dest, edge-triggered
		lo := uint32(32+i) | (1 << 16) // vector + masked
		a.writeIOAPIC(IOAPIC_REDTBL+i*2, lo)
		// High 32 bits: destination = CPU 0 (LAPIC ID 0)
		a.writeIOAPIC(IOAPIC_REDTBL+i*2+1, 0x00000000)
	}
}

// DispatchIRQ is called from the x86_64 exception handler when an
// external interrupt is received. It calls the registered handler
// and sends EOI.
//
//go:nosplit
func (a *APIC) DispatchIRQ(vector uint32) {
	if vector < 256 && a.handlers[vector] != nil {
		a.handlers[vector]()
	}
	a.EOI()
}

// === LAPIC register access ===

//go:nosplit
func (a *APIC) readLAPIC(offset uintptr) uint32 {
	return asm.MmioRead32(a.lapicBase + offset)
}

//go:nosplit
func (a *APIC) writeLAPIC(offset uintptr, value uint32) {
	asm.MmioWrite32(a.lapicBase+offset, value)
}

// === I/O APIC indirect register access ===

//go:nosplit
func (a *APIC) readIOAPIC(reg uint32) uint32 {
	asm.MmioWrite32(a.ioapicBase+IOAPIC_REGSEL, reg)
	return asm.MmioRead32(a.ioapicBase + IOAPIC_WIN)
}

//go:nosplit
func (a *APIC) writeIOAPIC(reg uint32, value uint32) {
	asm.MmioWrite32(a.ioapicBase+IOAPIC_REGSEL, reg)
	asm.MmioWrite32(a.ioapicBase+IOAPIC_WIN, value)
}
