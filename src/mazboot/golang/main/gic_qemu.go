//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	_ "unsafe" // Required for //go:linkname directives
)

//go:linkname read_id_aa64pfr0_el1 read_id_aa64pfr0_el1
//go:nosplit
//go:noinline
func read_id_aa64pfr0_el1() uint64

// GIC (Generic Interrupt Controller) for QEMU virt machine
// QEMU virt uses GICv2 by default
// Base addresses are initialized from linker symbols in gicInit()

// Global variables for GIC base addresses (set once during init)
var (
	gicDistBase uintptr // GIC Distributor base
	gicCpuBase  uintptr // GIC CPU Interface base
)

const (
	// GIC Distributor register offsets (from base)
	GICD_CTLR_OFFSET        = 0x000 // Distributor Control Register
	GICD_TYPER_OFFSET       = 0x004 // Interrupt Controller Type Register
	GICD_IGROUPRn_OFFSET    = 0x080 // Interrupt Group Registers (n = 0..31)
	GICD_ISENABLERn_OFFSET  = 0x100 // Interrupt Set-Enable Registers (n = 0..31)
	GICD_ICENABLERn_OFFSET  = 0x180 // Interrupt Clear-Enable Registers (n = 0..31)
	GICD_ISPENDRn_OFFSET    = 0x200 // Interrupt Set-Pending Registers (n = 0..31)
	GICD_ICPENDRn_OFFSET    = 0x280 // Interrupt Clear-Pending Registers (n = 0..31)
	GICD_ISACTIVERn_OFFSET  = 0x300 // Interrupt Set-Active Registers (n = 0..31)
	GICD_ICACTIVERn_OFFSET  = 0x380 // Interrupt Clear-Active Registers (n = 0..31)
	GICD_IPRIORITYRn_OFFSET = 0x400 // Interrupt Priority Registers (n = 0..254)
	GICD_ITARGETSRn_OFFSET  = 0x800 // Interrupt Target Registers (n = 0..254)
	GICD_ICFGRn_OFFSET      = 0xC00 // Interrupt Configuration Registers (n = 0..63)
	GICD_SGIR_OFFSET        = 0xF00 // Software Generated Interrupt Register

	// GIC CPU Interface register offsets (from CPU base, which is GIC_BASE + 0x10000)
	GICC_CTLR_OFFSET   = 0x000 // CPU Interface Control Register
	GICC_PMR_OFFSET    = 0x004 // Interrupt Priority Mask Register
	GICC_BPR_OFFSET    = 0x008 // Binary Point Register
	GICC_IAR_OFFSET    = 0x00C // Interrupt Acknowledge Register
	GICC_EOIR_OFFSET   = 0x010 // End of Interrupt Register
	GICC_RPR_OFFSET    = 0x014 // Running Priority Register
	GICC_HPPIR_OFFSET  = 0x018 // Highest Pending Interrupt Register
	GICC_ABPR_OFFSET   = 0x01C // Aliased Binary Point Register
	GICC_AIAR_OFFSET   = 0x020 // Aliased Interrupt Acknowledge Register
	GICC_AEOIR_OFFSET  = 0x024 // Aliased End of Interrupt Register
	GICC_AHPPIR_OFFSET = 0x028 // Aliased Highest Pending Interrupt Register
	GICC_APR_OFFSET    = 0x0D0 // Active Priority Register
	GICC_NSAPR_OFFSET  = 0x0E0 // Non-secure Active Priority Register
	GICC_IIDR_OFFSET   = 0x0FC // CPU Interface Identification Register

	// Interrupt IDs
	// PPIs (Private Peripheral Interrupts): 16-31
	// SPIs (Shared Peripheral Interrupts): 32-1019
	IRQ_ID_TIMER_PPI          = 27 // ARM Generic Timer PPI - Virtual Timer (CNTV) for EL1 - ID 27
	IRQ_ID_TIMER_PHYSICAL_PPI = 30 // Physical Timer (CNTP) - ID 30 (for experiments)
	IRQ_ID_UART_SPI           = 33 // Try interrupt 33 (should be safe SPI)
)

// Interrupt handler function type
type InterruptHandler func()

var (
	// Array of interrupt handlers (indexed by interrupt ID)
	// We support up to 1020 interrupts (0-1019)
	interruptHandlers [1020]InterruptHandler
)

// gicInit initializes the Generic Interrupt Controller
// REVERTED TO FULL INIT: Minimal init experiment didn't solve the spurious 1022 issue
//
//go:nosplit
func gicInit() {
	// Initialize GIC base addresses from linker symbols (once)
	gicDistBase = getLinkerSymbol("__gic_base")
	gicCpuBase = gicDistBase + 0x10000 // CPU interface is at +64KB from distributor

	// Call the full initialization
	gicInitFull()
}

// gicInitFull is the original full initialization (kept for easy revert)
//
//go:nosplit
func gicInitFull() {
	// Compute register addresses from global base addresses
	GICD_CTLR := gicDistBase + GICD_CTLR_OFFSET
	GICD_ICPENDRn := gicDistBase + GICD_ICPENDRn_OFFSET
	GICD_IGROUPRn := gicDistBase + GICD_IGROUPRn_OFFSET
	GICD_IPRIORITYRn := gicDistBase + GICD_IPRIORITYRn_OFFSET
	GICD_ITARGETSRn := gicDistBase + GICD_ITARGETSRn_OFFSET
	GICD_ICFGRn := gicDistBase + GICD_ICFGRn_OFFSET
	GICC_CTLR := gicCpuBase + GICC_CTLR_OFFSET
	GICC_PMR := gicCpuBase + GICC_PMR_OFFSET
	GICC_BPR := gicCpuBase + GICC_BPR_OFFSET

	// Disable distributor and CPU interface
	asm.MmioWrite(GICD_CTLR, 0)
	asm.MmioWrite(GICC_CTLR, 0)

	// Set priority mask to allow all interrupts
	asm.MmioWrite(GICC_PMR, 0xFF)
	asm.MmioWrite(GICC_BPR, 0)

	// Clear all pending interrupts
	for i := 0; i < 32; i++ {
		asm.MmioWrite(GICD_ICPENDRn+uintptr(i*4), 0xFFFFFFFF)
	}

	// Route all interrupts to Group 0 (secure) for QEMU virt compatibility
	for i := 0; i < 32; i++ {
		asm.MmioWrite(GICD_IGROUPRn+uintptr(i*4), 0x00000000)
	}

	// Set interrupt priorities (0x80 = medium priority)
	for i := 0; i < 256; i++ {
		asm.MmioWrite(GICD_IPRIORITYRn+uintptr(i*4), 0x80808080)
	}

	// Route all interrupts to CPU 0
	for i := 0; i < 256; i++ {
		asm.MmioWrite(GICD_ITARGETSRn+uintptr(i*4), 0x01010101)
	}

	// Configure interrupts as level-triggered
	for i := 0; i < 64; i++ {
		asm.MmioWrite(GICD_ICFGRn+uintptr(i*4), 0)
	}

	// Enable distributor and CPU interface (Group 0 only)
	asm.MmioWrite(GICD_CTLR, 0x01)
	asm.MmioWrite(GICC_CTLR, 0x01)
}

// checkSecurityState checks if we're running in Secure or Non-secure EL1 (debugging only)
//
//go:nosplit
func checkSecurityState() {
	// Not used during normal operation - kept for debugging
}

// gicEnableInterrupt enables a specific interrupt in the GIC
//
//go:nosplit
func gicEnableInterrupt(irqID uint32) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}

	// Compute register address from global base
	GICD_ISENABLERn := gicDistBase + GICD_ISENABLERn_OFFSET

	// Calculate register index (32 interrupts per register)
	regIndex := irqID / 32
	bitIndex := irqID % 32

	// Set enable bit in ISENABLER register
	asm.MmioWrite(GICD_ISENABLERn+uintptr(regIndex*4), 1<<bitIndex)
}

// gicDisableInterrupt disables a specific interrupt in the GIC
//
//go:nosplit
func gicDisableInterrupt(irqID uint32) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}

	// Compute register address from global base
	GICD_ICENABLERn := gicDistBase + GICD_ICENABLERn_OFFSET

	// Calculate register index (32 interrupts per register)
	regIndex := irqID / 32
	bitIndex := irqID % 32

	// Clear enable bit in ICENABLER register
	asm.MmioWrite(GICD_ICENABLERn+uintptr(regIndex*4), 1<<bitIndex)
}

// gicHandleInterruptWithID handles an interrupt with a pre-acknowledged ID
// Used when assembly has already read GICC_IAR to avoid timing issues
//
//go:nosplit
func gicHandleInterruptWithID(irqID uint32) {
	if irqID >= 1020 {
		return
	}
	if interruptHandlers[irqID] != nil {
		interruptHandlers[irqID]()
	}
	gicEndOfInterrupt(irqID)
}

// gicAcknowledgeInterrupt reads the interrupt ID from the CPU interface
// Returns the interrupt ID (bits 9:0) or 1023 if spurious
//
//go:nosplit
func gicAcknowledgeInterrupt() uint32 {
	// Compute register address from global CPU base
	GICC_IAR := gicCpuBase + GICC_IAR_OFFSET

	// Read IAR (Interrupt Acknowledge Register)
	// Bits 9:0 = interrupt ID
	// Bit 10 = CPU ID (for SGIs)
	// 1023 = spurious interrupt
	iar := asm.MmioRead(GICC_IAR)
	return iar & 0x3FF // Return interrupt ID (bits 9:0)
}

// gicEndOfInterrupt signals end of interrupt handling
//
//go:nosplit
func gicEndOfInterrupt(irqID uint32) {
	// Compute register address from global CPU base
	GICC_EOIR := gicCpuBase + GICC_EOIR_OFFSET

	// Write interrupt ID to EOIR (End of Interrupt Register)
	asm.MmioWrite(GICC_EOIR, irqID)
}

// registerInterruptHandler registers a handler function for a specific interrupt
//
//go:nosplit
func registerInterruptHandler(irqID uint32, handler InterruptHandler) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}
	interruptHandlers[irqID] = handler
}

var interruptsEnabled bool

// gicHandleInterrupt handles an interrupt from the GIC
// This is called from irqHandlerGo (Go IRQ handler)
//
//go:nosplit
//go:noinline
func gicHandleInterrupt() {
	// Print 'H' to show we entered gicHandleInterrupt
	printChar('H')

	// Note: Interrupts should already be enabled before timer starts
	// Do NOT enable interrupts from inside the interrupt handler!

	// Acknowledge interrupt and get ID
	irqID := gicAcknowledgeInterrupt()
	printChar('A')

	// Check for spurious interrupt (ID 1023)
	if irqID >= 1020 {
		return // Spurious interrupt, ignore
	}

	// Call registered handler if available
	if interruptHandlers[irqID] != nil {
		// Call handler
		interruptHandlers[irqID]()
	} else {
		// No handler registered - log it using direct UART to avoid ring buffer issues
		asm.UartPutcPl011('U') // 'U' for Unhandled
		asm.UartPutcPl011('I') // 'I' for Interrupt
		asm.UartPutcPl011(':') // ':'
		asm.UartPutcPl011(' ')
		// Simple digit output for interrupt ID
		if irqID < 10 {
			asm.UartPutcPl011(byte('0' + irqID))
		} else {
			asm.UartPutcPl011(byte('0' + irqID/10))
			asm.UartPutcPl011(byte('0' + irqID%10))
		}
		asm.UartPutcPl011('\r')
		asm.UartPutcPl011('\n')
	}

	// Signal end of interrupt
	gicEndOfInterrupt(irqID)
}
