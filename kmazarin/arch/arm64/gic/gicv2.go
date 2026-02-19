
package gic

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
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
	distReg := node.Reg[0]
	cpuReg := node.Reg[1]

	// Map both MMIO regions before accessing hardware.
	// GICv2 has two regions: distributor (GICD) and CPU interface (GICC).
	if err := kmem.MapDeviceMMIO(distReg.Address, distReg.Size); err != nil {
		return nil, err
	}
	if err := kmem.MapDeviceMMIO(cpuReg.Address, cpuReg.Size); err != nil {
		return nil, err
	}

	// Convert physical address to high-memory kernel address
	// Physical: 0x08000000 → Kernel: 0xFFFFFFFF08000000
	// This matches what the exception handler uses for GIC access
	const KernelMMIOOffset = 0xFFFFFFFF00000000
	gic := &GICv2{
		distBase: distReg.Address + KernelMMIOOffset, // Distributor (high memory)
		cpuBase:  cpuReg.Address + KernelMMIOOffset,  // CPU interface (high memory)
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
	if irq < 1020 {
		g.handlers[irq] = handler
		// Also register with kirq dispatch table so the exception handler
		// can dispatch this IRQ. The exception handler calls kirq.DispatchIRQ
		// which looks up handlers in kirq's table, not ours.
		kirq.RegisterSimpleHandler(irq, handler)
	}
}

//go:nosplit
func (g *GICv2) EnableIRQ(irq uint32) {
	reg := uintptr(irq / 32)
	bit := irq % 32
	offset := GICD_ISENABLER + (reg * 4)
	g.writeDistReg(offset, 1<<bit)
}

// SetIRQTarget sets the CPU target mask for an IRQ.
// For GICv2, target is a bitmask of CPUs (bit 0 = CPU 0).
//
//go:nosplit
func (g *GICv2) SetIRQTarget(irq uint32, cpuMask uint8) {
	// GICD_ITARGETSR: 4 IRQs per 32-bit register, use read-modify-write
	regOffset := uintptr(0x800) + uintptr(irq/4)*4
	bytePos := irq % 4
	shift := bytePos * 8
	val := g.readDistReg(regOffset)
	val = (val &^ (0xFF << shift)) | (uint32(cpuMask) << shift)
	g.writeDistReg(regOffset, val)
}

// SetIRQPriority sets the priority for an IRQ.
// Lower values = higher priority. Timer uses 0x00, default is 0xA0.
//
//go:nosplit
func (g *GICv2) SetIRQEdgeTriggered(irq uint32) {
	// GICD_ICFGR: 2 bits per IRQ, bit[1] = 1 means edge-triggered
	reg := uintptr(irq / 16)
	shift := (irq % 16) * 2
	offset := uintptr(0xC00) + reg*4
	val := g.readDistReg(offset)
	val |= (0x2 << shift) // Set bit[1] for edge-triggered
	g.writeDistReg(offset, val)
}

// SetIRQLevelTriggered is a no-op on GICv2.
// GIC trigger mode for SPIs is typically configured once during init.
//
//go:nosplit
func (g *GICv2) SetIRQLevelTriggered(_ uint32) {}

//go:nosplit
func (g *GICv2) SetIRQPriority(irq uint32, priority uint8) {
	// GICD_IPRIORITYR: 1 byte per IRQ, byte-accessible
	offset := GICD_IPRIORITYR + uintptr(irq)
	asm.MmioWrite8(g.distBase+offset, priority)
}

//go:nosplit
func (g *GICv2) DisableIRQ(irq uint32) {
	reg := uintptr(irq / 32)
	bit := irq % 32
	offset := GICD_ICENABLER + (reg * 4)
	g.writeDistReg(offset, 1<<bit)

	// Clear any pending interrupts for this IRQ
	// GICD_ICPENDR (0x280) is write-1-to-clear
	g.writeDistReg(0x280+(reg*4), 1<<bit)
}

// DumpIRQState prints the GIC configuration for a specific IRQ for debugging.
func (g *GICv2) DumpIRQState(irq uint32) {
	reg := uintptr(irq / 32)
	bit := irq % 32

	isenabler := g.readDistReg(GICD_ISENABLER + reg*4)
	enabled := (isenabler >> bit) & 1

	igroupr := g.readDistReg(0x080 + reg*4)
	group := (igroupr >> bit) & 1

	itargets := g.readDistReg(0x800 + uintptr(irq/4)*4)
	target := (itargets >> ((irq % 4) * 8)) & 0xFF

	ipriority := g.readDistReg(GICD_IPRIORITYR + uintptr(irq/4)*4)
	priority := (ipriority >> ((irq % 4) * 8)) & 0xFF

	icfgr := g.readDistReg(0xC00 + uintptr(irq/16)*4)
	cfg := (icfgr >> ((irq % 16) * 2)) & 0x3

	ispendr := g.readDistReg(0x200 + reg*4)
	pending := (ispendr >> bit) & 1

	// Also read target byte directly
	tgtByte := asm.MmioRead8(g.distBase + 0x800 + uintptr(irq))
	console.KPrintf("[GIC] IRQ %d: en=%d grp=%d tgt=0x%x(byte=0x%x) pri=0x%x cfg=%d pend=%d\n",
		irq, enabled, group, target, uint32(tgtByte), priority, cfg, pending)
}

// Hardware initialization
func (g *GICv2) initHardware() {
	// Check if GIC is already initialized
	// QEMU virt machine pre-initializes GIC with Group 0 (Secure) interrupts:
	//   - GICD_CTLR = 0x01 (only Group 0 enabled)
	//   - GICC_CTLR = 0x01 (only Group 0 enabled)
	//   - GICD_IGROUPR = 0x00 (all interrupts are Group 0)
	//
	// PROBLEM: Kmazarin runs in Non-Secure EL1, which can ONLY receive Group 1 interrupts!
	// FIX: Reconfigure GIC for Non-Secure EL1 by moving all interrupts to Group 1.
	gicdCtrl := g.readDistReg(GICD_CTLR)
	if gicdCtrl != 0 {
		// Keep all interrupts in Group 0. QEMU virt runs without TrustZone
		// (no secure=on), so there's no Non-Secure distinction. Group 0
		// interrupts are delivered via IRQ to EL1 and GICC_IAR works normally.
		// Moving to Group 1 breaks ITARGETSR (becomes RAZ/WI for Group 1
		// in QEMU's GICv2 without security extensions).

		// Set timer IRQ (27) to HIGHEST priority (0x00)
		ipri6 := g.readDistReg(0x418)  // GICD_IPRIORITYR6 (IRQ 24-27)
		ipri6 = (ipri6 & 0x00FFFFFF) | 0x00000000
		g.writeDistReg(0x418, ipri6)

		// Set UART IRQ 33 to medium priority (0x80)
		ipri8 := g.readDistReg(0x420)  // GICD_IPRIORITYR8 (IRQ 32-35)
		ipri8 = (ipri8 & 0xFFFF00FF) | 0x00008000
		g.writeDistReg(0x420, ipri8)

		// Configure timer IRQ (27) as EDGE-TRIGGERED
		icfg1 := g.readDistReg(0xC04)
		icfg1 = (icfg1 & 0xFF3FFFFF) | (0x2 << 22)
		g.writeDistReg(0xC04, icfg1)

		// Route all SPIs to CPU 0. Cardinal/QEMU leaves ITARGETSR at 0
		// for SPIs (IRQ 32+), so MSI-X interrupts never reach the CPU.
		// Use 32-bit writes (4 IRQs per register) for reliable MMIO access.
		for i := uintptr(8); i < 72; i++ { // registers 8-71 cover IRQs 32-287
			g.writeDistReg(0x800+i*4, 0x01010101)
		}

		// CRITICAL: Ensure PMR allows all priorities.
		// Cardinal may leave PMR at a restrictive value, which silently
		// filters SPIs configured with priority > PMR (e.g. 0xA0).
		// The timer (priority 0x00) still passes, hiding this bug.
		g.writeCPUReg(GICC_PMR, 0xFF)

		return
	}

	// GIC not yet initialized - perform full initialization
	// CRITICAL: Disable CPU interface FIRST to stop interrupt delivery
	g.writeCPUReg(GICC_CTLR, 0)

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

	// Clear all active interrupts
	// This ensures no active interrupts from Cardinal are stuck
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x380+(i*4), 0xFFFFFFFF) // GICD_ICACTIVER
	}

	// CRITICAL: Route all interrupts to Group 0 (secure)
	// This matches Cardinal's initialization and is required for proper delivery
	// Without this, interrupts default to Group 1 and may not be delivered
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x080+(i*4), 0x00000000) // GICD_IGROUPR - all Group 0
	}

	// Set all interrupts to medium priority (0x80, matches Cardinal)
	for i := uintptr(0); i < 256; i++ {
		g.writeDistReg(GICD_IPRIORITYR+(i*4), 0x80808080)
	}

	// CRITICAL: Set timer IRQ (27) to HIGHEST priority (0x00)
	// IRQ 27 is in GICD_IPRIORITYR6, byte 3
	// Read-modify-write to only change byte 3
	ipri6 := g.readDistReg(GICD_IPRIORITYR + 24)  // Register 6 (IRQ 24-27)
	ipri6 = (ipri6 & 0x00FFFFFF) | (0x00 << 24)    // Set byte 3 to 0x00
	g.writeDistReg(GICD_IPRIORITYR+24, ipri6)

	// CRITICAL: Route all interrupts to CPU 0 (matches Cardinal's init)
	// Even for PPIs (IRQ 16-31), QEMU/GICv2 requires GICD_ITARGETSR to be set
	for i := uintptr(0); i < 256; i++ {
		g.writeDistReg(0x800+(i*4), 0x01010101) // GICD_ITARGETSR
	}

	// CRITICAL: Configure all interrupts as level-triggered (matches Cardinal)
	// GICD_ICFGR: 2 bits per interrupt, 0b00 = level-triggered
	for i := uintptr(0); i < 64; i++ {
		g.writeDistReg(0xC00+(i*4), 0x00000000) // GICD_ICFGR - all level-triggered
	}

	// CRITICAL: Configure timer IRQ (27) as EDGE-TRIGGERED
	// ARM Generic Timer generates edge interrupts, not level. Using level-triggered
	// configuration causes the GIC to malfunction after ~600 interrupts.
	// GICD_ICFGR1 (offset 0xC04) covers IRQs 16-31, 2 bits per IRQ
	// IRQ 27 = bits [23:22], 0b10 = edge-triggered
	icfg1 := g.readDistReg(0xC04)
	icfg1 = (icfg1 & 0xFF3FFFFF) | (0x2 << 22)  // Set bits [23:22] = 0b10 (edge)
	g.writeDistReg(0xC04, icfg1)

	// Enable distributor for Group 0 only (matches Cardinal)
	// Bit 0 = Enable Group 0
	// Since all interrupts are assigned to Group 0, we only enable that group
	g.writeDistReg(GICD_CTLR, 0x01)  // Enable Group 0 only

	// Configure CPU interface
	g.writeCPUReg(GICC_PMR, 0xFF) // Set priority mask to 0xFF (allow ALL priorities)
	g.writeCPUReg(0x008, 0)       // GICC_BPR = 0 (matches Cardinal)

	// CRITICAL: Enable Group 0 interrupts at CPU interface (matches Cardinal)
	// Bit 0 = EnableGrp0 (Group 0 interrupts signal IRQ)
	// Since all interrupts are in Group 0, we only enable Group 0
	g.writeCPUReg(GICC_CTLR, 0x01)  // Enable Group 0 only

	// NOTE: Timer IRQ (27) will be explicitly enabled later via EnableTimerIRQ()
	// We don't disable it here since we'll need it for async preemption
}

// Hardware access
//
//go:nosplit
func (g *GICv2) readDistReg(offset uintptr) uint32 {
	return asm.MmioRead32(g.distBase + offset)
}

//go:nosplit
func (g *GICv2) writeDistReg(offset uintptr, value uint32) {
	asm.MmioWrite32(g.distBase+offset, value)
	// NOTE: Barriers removed - Cardinal doesn't use them and they may interfere
	// with rapid MMIO writes during GIC configuration
	// asm_dsb_sy()
	// asm_isb()
}

// asm_dsb_sy performs a data synchronization barrier (system scope)
// Implemented in gicv2_arm64.s
//
//go:nosplit
func asm_dsb_sy()

// asm_isb performs an instruction synchronization barrier
// Implemented in gicv2_arm64.s
//
//go:nosplit
func asm_isb()

//go:nosplit
func (g *GICv2) readCPUReg(offset uintptr) uint32 {
	return asm.MmioRead32(g.cpuBase + offset)
}

//go:nosplit
func (g *GICv2) writeCPUReg(offset uintptr, value uint32) {
	asm.MmioWrite32(g.cpuBase+offset, value)
	// NOTE: Barriers removed - Cardinal doesn't use them and they may interfere
	// with rapid MMIO writes during GIC configuration
	// asm_dsb_sy()
	// asm_isb()
}

// DispatchIRQ is called from assembly exception handler
//
//go:nosplit
func (g *GICv2) DispatchIRQ() {
	// Read interrupt ID
	irq := g.readCPUReg(GICC_IAR) & 0x3FF

	// Call handler if registered
	if irq < 1020 && g.handlers[irq] != nil {
		g.handlers[irq]()
	}

	// Signal end of interrupt
	g.writeCPUReg(GICC_EOIR, irq)
}
