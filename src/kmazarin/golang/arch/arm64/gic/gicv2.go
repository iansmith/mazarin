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
	// DEBUG: Print that Init was called - use very distinctive pattern
	const uartBase = 0xFFFFFFFF09000000
	for i := 0; i < 10; i++ {
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'X'
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// Convert physical address to high-memory kernel address
	// Physical: 0x08000000 → Kernel: 0xFFFFFFFF08000000
	// This matches what the exception handler uses for GIC access
	const KernelMMIOOffset = 0xFFFFFFFF00000000
	gic := &GICv2{
		distBase: node.Reg[0].Address + KernelMMIOOffset, // Distributor (high memory)
		cpuBase:  node.Reg[1].Address + KernelMMIOOffset, // CPU interface (high memory)
	}

	// DEBUG: Print after struct creation
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'S'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'R'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'

	// Initialize GIC hardware
	gic.initHardware()

	// DEBUG: Print after initHardware
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'D'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'O'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'N'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'E'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'

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

	// Clear any pending interrupts for this IRQ
	// GICD_ICPENDR (0x280) is write-1-to-clear
	g.writeDistReg(0x280+(reg*4), 1<<bit)
}

// Hardware initialization
func (g *GICv2) initHardware() {
	const uartBase = 0xFFFFFFFF09000000

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
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'G'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'R'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'P'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '1'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'

		// CRITICAL: Move all interrupts to Group 1 (Non-Secure)
		// GICv2 spec: Group 0 = Secure, Group 1 = Non-Secure
		// In Non-Secure EL1, we can ONLY receive Group 1 interrupts
		// Set GICD_IGROUPRn bits to 1 for all interrupts
		for i := uintptr(0); i < 32; i++ {
			g.writeDistReg(0x080+(i*4), 0xFFFFFFFF) // GICD_IGROUPR
		}

		// Enable both Group 0 and Group 1 in distributor
		// Bit 0 = EnableGrp0, Bit 1 = EnableGrp1
		g.writeDistReg(GICD_CTLR, 0x03)

		// Enable both Group 0 and Group 1 in CPU interface
		// Bit 0 = EnableGrp0, Bit 1 = EnableGrp1, Bit 2 = AckCtl
		// AckCtl=1 allows GICC_IAR to acknowledge both Group 0 and Group 1 interrupts
		// (otherwise GICC_IAR only acknowledges Group 0, and GICC_AIAR is needed for Group 1)
		g.writeCPUReg(GICC_CTLR, 0x07) // 0x07 = EnableGrp0 | EnableGrp1 | AckCtl

		// CRITICAL: Set timer IRQ (27) to HIGHEST priority (0x00)
		// This ensures timer can preempt other interrupts (like UART at 0x80)
		// Without this, if UART IRQ is active, timer can't preempt and we deadlock!
		// IRQ 27 is in GICD_IPRIORITYR6 (offset 0x418), byte 3
		// Other interrupts default to 0xA0, set UART (33) to 0x80
		ipri6 := g.readDistReg(0x418)  // GICD_IPRIORITYR6 (IRQ 24-27)
		ipri6 = (ipri6 & 0x00FFFFFF) | 0x00000000  // Set byte 3 (IRQ 27) to 0x00
		g.writeDistReg(0x418, ipri6)

		// Set UART IRQ 33 to medium priority (0x80) - lower than timer
		// IRQ 33 is in GICD_IPRIORITYR8 (offset 0x420), byte 1
		ipri8 := g.readDistReg(0x420)  // GICD_IPRIORITYR8 (IRQ 32-35)
		ipri8 = (ipri8 & 0xFFFF00FF) | 0x00008000  // Set byte 1 (IRQ 33) to 0x80
		g.writeDistReg(0x420, ipri8)

		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'P'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'R'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'I'
		*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'
		return
	}

	// GIC not yet initialized - perform full initialization
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'I'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'N'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'I'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'

	// CRITICAL: Disable CPU interface FIRST to stop interrupt delivery
	g.writeCPUReg(GICC_CTLR, 0)

	// Disable distributor
	g.writeDistReg(GICD_CTLR, 0)

	// TEST: Verify write took effect
	testVal2 := g.readDistReg(GICD_CTLR)
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'F'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '='
	// Print testVal2 in hex
	for shift := 28; shift >= 0; shift -= 4 {
		nibble := (testVal2 >> uint(shift)) & 0xF
		if nibble < 10 {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '0' + uint32(nibble)
		} else {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A' + uint32(nibble-10)
		}
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// DEBUG: Mark we're about to start configuration loops
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '['
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'L'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'O'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'O'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'P'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'S'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = ']'

	// Disable all interrupts (clear all ISENABLER registers)
	// This ensures no interrupts from Cardinal are still enabled
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(GICD_ICENABLER+(i*4), 0xFFFFFFFF)
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'D' // After disable loop

	// Clear all pending interrupts
	// This ensures no pending interrupts from Cardinal are latched
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x280+(i*4), 0xFFFFFFFF) // GICD_ICPENDR
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'P' // After pending clear loop

	// Clear all active interrupts
	// This ensures no active interrupts from Cardinal are stuck
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x380+(i*4), 0xFFFFFFFF) // GICD_ICACTIVER
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A' // After active clear loop

	// CRITICAL: Route all interrupts to Group 0 (secure)
	// This matches Cardinal's initialization and is required for proper delivery
	// Without this, interrupts default to Group 1 and may not be delivered
	for i := uintptr(0); i < 32; i++ {
		g.writeDistReg(0x080+(i*4), 0x00000000) // GICD_IGROUPR - all Group 0
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'G' // After group assignment loop

	// Set all interrupts to medium priority (0x80, matches Cardinal)
	for i := uintptr(0); i < 256; i++ {
		g.writeDistReg(GICD_IPRIORITYR+(i*4), 0x80808080)
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'M' // After priority loop

	// CRITICAL: Set timer IRQ (27) to HIGHEST priority (0x00)
	// IRQ 27 is in GICD_IPRIORITYR6, byte 3
	// Read-modify-write to only change byte 3
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '1'
	ipri6 := g.readDistReg(GICD_IPRIORITYR + 24)  // Register 6 (IRQ 24-27)
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '2'
	ipri6 = (ipri6 & 0x00FFFFFF) | (0x00 << 24)    // Set byte 3 to 0x00
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '3'
	g.writeDistReg(GICD_IPRIORITYR+24, ipri6)
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '4'

	// DEBUG: Mark before ITARGETSR loop
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'B'

	// CRITICAL: Route all interrupts to CPU 0 (matches Cardinal's init)
	// Even for PPIs (IRQ 16-31), QEMU/GICv2 requires GICD_ITARGETSR to be set
	for i := uintptr(0); i < 256; i++ {
		g.writeDistReg(0x800+(i*4), 0x01010101) // GICD_ITARGETSR
	}

	// DEBUG: Check if we reach this point
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'K'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'G'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '1'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// Read back GICD_ITARGETSR6 to verify write took effect
	itargetsr6_after := g.readDistReg(0x800 + 24)

	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'K'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'G'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '2'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '='
	for shift := 28; shift >= 0; shift -= 4 {
		nibble := (itargetsr6_after >> uint(shift)) & 0xF
		if nibble < 10 {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '0' + uint32(nibble)
		} else {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A' + uint32(nibble-10)
		}
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// CRITICAL: Configure all interrupts as level-triggered (matches Cardinal)
	// GICD_ICFGR: 2 bits per interrupt, 0b00 = level-triggered
	for i := uintptr(0); i < 64; i++ {
		g.writeDistReg(0xC00+(i*4), 0x00000000) // GICD_ICFGR - all level-triggered
	}

	// Enable distributor for Group 0 only (matches Cardinal)
	// Bit 0 = Enable Group 0
	// Since all interrupts are assigned to Group 0, we only enable that group
	g.writeDistReg(GICD_CTLR, 0x01)  // Enable Group 0 only
	// Verify it took effect
	gicdCtrl = g.readDistReg(GICD_CTLR)

	// DEBUG: Print current GIC configuration via direct UART
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'D'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'C'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'R'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '='
	// Print gicdCtrl in hex
	for shift := 28; shift >= 0; shift -= 4 {
		nibble := (gicdCtrl >> uint(shift)) & 0xF
		if nibble < 10 {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '0' + uint32(nibble)
		} else {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A' + uint32(nibble-10)
		}
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// Configure CPU interface
	g.writeCPUReg(GICC_PMR, 0xFF) // Set priority mask to 0xFF (allow ALL priorities)
	g.writeCPUReg(0x008, 0)       // GICC_BPR = 0 (matches Cardinal)

	// CRITICAL: Enable Group 0 interrupts at CPU interface (matches Cardinal)
	// Bit 0 = EnableGrp0 (Group 0 interrupts signal IRQ)
	// Since all interrupts are in Group 0, we only enable Group 0
	g.writeCPUReg(GICC_CTLR, 0x01)  // Enable Group 0 only
	// Verify it took effect
	giccCtrl := g.readCPUReg(GICC_CTLR)
	giccPmr := g.readCPUReg(GICC_PMR)
	giccBpr := g.readCPUReg(0x008) // GICC_BPR

	// DEBUG: Print CPU interface configuration
	println("GIC Init: GICC_CTLR =", hex(giccCtrl))
	println("GIC Init: GICC_PMR =", hex(giccPmr))
	println("GIC Init: GICC_BPR =", hex(giccBpr))

	// DEBUG: Check IRQ 27 interrupt group assignment (GICD_IGROUPR0 covers IRQ 0-31)
	igroupr0 := g.readDistReg(0x080) // GICD_IGROUPR0
	println("GIC Init: GICD_IGROUPR0 =", hex(igroupr0), "bit27=", (igroupr0>>27)&1)

	// DEBUG: Check GICD_ITARGETSR6 as read by CPU (should show 0x01010101 for CPU 0)
	itargetsr6 := g.readDistReg(0x800 + 24) // Register 6 (IRQ 24-27)
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'G'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'T'
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '='
	for shift := 28; shift >= 0; shift -= 4 {
		nibble := (itargetsr6 >> uint(shift)) & 0xF
		if nibble < 10 {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '0' + uint32(nibble)
		} else {
			*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = 'A' + uint32(nibble-10)
		}
	}
	*(*uint32)(unsafe.Pointer(uintptr(uartBase))) = '\n'

	// NOTE: Timer IRQ (27) will be explicitly enabled later via EnableTimerIRQ()
	// We don't disable it here since we'll need it for async preemption
}

// hex formats a uint32 as hex string
func hex(v uint32) string {
	const digits = "0123456789abcdef"
	buf := [10]byte{'0', 'x'}
	for i := 7; i >= 0; i-- {
		buf[2+i] = digits[v&0xF]
		v >>= 4
	}
	return string(buf[:])
}

// Hardware access
func (g *GICv2) readDistReg(offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(g.distBase + offset))
}

func (g *GICv2) writeDistReg(offset uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(g.distBase + offset)) = value
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

func (g *GICv2) readCPUReg(offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(g.cpuBase + offset))
}

func (g *GICv2) writeCPUReg(offset uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(g.cpuBase + offset)) = value
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
	if irq < 1024 && g.handlers[irq] != nil {
		g.handlers[irq]()
	}

	// Signal end of interrupt
	g.writeCPUReg(GICC_EOIR, irq)
}
