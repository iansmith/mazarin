//go:build qemuvirt && aarch64

package gic

import (
	"kmazarin/deviceapi"
	"kmazarin/dtb"
	"kmazarin/kirq"
	"unsafe"
)

// GICv2 register offsets
const (
	// Distributor registers
	GICD_CTLR       = 0x000 // Control register
	GICD_ISENABLER  = 0x100 // Interrupt set-enable registers
	GICD_ICENABLER  = 0x180 // Interrupt clear-enable registers
	GICD_IPRIORITYR = 0x400 // Interrupt priority registers

	// CPU interface registers
	GICC_CTLR = 0x000 // Control register
	GICC_PMR  = 0x004 // Priority mask register
	GICC_IAR  = 0x00C // Interrupt acknowledge register
	GICC_EOIR = 0x010 // End of interrupt register
)

// GICv2Driver implements device.Discoverable
type GICv2Driver struct{}

func (d *GICv2Driver) Compatible() []string {
	return []string{"arm,gic-400", "arm,cortex-a15-gic"}
}

func (d *GICv2Driver) Probe(node *dtb.Node) bool {
	// GICv2 needs two register regions (distributor and CPU interface)
	return node.Reg != nil && len(node.Reg) >= 2
}

func (d *GICv2Driver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	// Use physical addresses directly - high memory mapping appears to have issues
	// with GIC ISENABLER writes even though UART high-memory works
	gic := &GICv2{
		distBase: node.Reg[0].Address, // Distributor (physical)
		cpuBase:  node.Reg[1].Address, // CPU interface (physical)
	}

	// Initialize GIC hardware
	gic.initHardware()

	return gic, nil
}

// GICv2 implements InterruptController interface
type GICv2 struct {
	distBase uintptr
	cpuBase  uintptr
	handlers [1024]func()
}

// Closable implementation
func (g *GICv2) Name() string {
	return "gicv2"
}

func (g *GICv2) Close() error {
	// Disable GIC
	g.writeDistReg(GICD_CTLR, 0)
	g.writeCPUReg(GICC_CTLR, 0)
	return nil
}

// InterruptController implementation
func (g *GICv2) RegisterHandler(irq uint32, handler func()) {
	if irq < 1024 {
		g.handlers[irq] = handler
		// Also register with kirq dispatch table so the exception handler
		// can dispatch this IRQ. The exception handler calls kirq.DispatchNonTimerIRQ
		// which looks up handlers in kirq's table, not ours.
		kirq.RegisterSimpleHandler(irq, handler)
	}
}

func (g *GICv2) EnableIRQ(irq uint32) {
	reg := uintptr(irq / 32)
	bit := irq % 32
	offset := GICD_ISENABLER + (reg * 4)
	g.writeDistReg(offset, 1<<bit)
}

func (g *GICv2) DisableIRQ(irq uint32) {
	reg := uintptr(irq / 32)
	bit := irq % 32
	offset := GICD_ICENABLER + (reg * 4)
	g.writeDistReg(offset, 1<<bit)
}

// Hardware initialization
func (g *GICv2) initHardware() {
	// Disable distributor
	g.writeDistReg(GICD_CTLR, 0)

	// Disable all interrupts (clear all ISENABLER registers)
	// This ensures no interrupts from Cardinal are still enabled
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(GICD_ICENABLER+(i*4), 0xFFFFFFFF)
	}

	// Clear all pending interrupts
	// This ensures no pending interrupts from Cardinal are latched
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x280+(i*4), 0xFFFFFFFF) // GICD_ICPENDR
	}

	// Set all interrupts to lowest priority
	for i := uintptr(0); i < 256; i++ {
		g.writeDistReg(GICD_IPRIORITYR+(i*4), 0xA0A0A0A0)
	}

	// Enable distributor
	g.writeDistReg(GICD_CTLR, 1)

	// Configure CPU interface
	g.writeCPUReg(GICC_PMR, 0xF0) // Set priority mask
	g.writeCPUReg(GICC_CTLR, 1)   // Enable CPU interface

	// CRITICAL: Ensure timer IRQ (27) stays disabled
	// Cardinal enabled it, and even though we cleared all ISENABLERs above,
	// we explicitly disable it again to be safe
	g.DisableIRQ(27)
}

// Hardware access
func (g *GICv2) readDistReg(offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(g.distBase + offset))
}

func (g *GICv2) writeDistReg(offset uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(g.distBase + offset)) = value
}

func (g *GICv2) readCPUReg(offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(g.cpuBase + offset))
}

func (g *GICv2) writeCPUReg(offset uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(g.cpuBase + offset)) = value
}

// DispatchIRQ is called from assembly exception handler
//
//go:nosplit
func (g *GICv2) DispatchIRQ() {
	// Read interrupt ID
	irq := g.readCPUReg(GICC_IAR) & 0x3FF

	// Call handler if registered
	if irq < 1024 && g.handlers[irq] != nil {
		g.handlers[irq]()
	}

	// Signal end of interrupt
	g.writeCPUReg(GICC_EOIR, irq)
}
