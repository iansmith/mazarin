//go:build qemuvirt && aarch64

package main

import (
	_ "unsafe" // Required for //go:linkname directives
)

//go:linkname read_id_aa64pfr0_el1 read_id_aa64pfr0_el1
//go:nosplit
//go:noinline
func read_id_aa64pfr0_el1() uint64

// GIC (Generic Interrupt Controller) for QEMU virt machine
// QEMU virt uses GICv2 by default
// Base addresses:
//   GIC Distributor: 0x08000000
//   GIC CPU Interface: 0x08010000

const (
	// GIC Distributor base address
	GIC_DIST_BASE = 0x08000000

	// GIC Distributor register offsets
	GICD_CTLR        = GIC_DIST_BASE + 0x000 // Distributor Control Register
	GICD_TYPER       = GIC_DIST_BASE + 0x004 // Interrupt Controller Type Register
	GICD_IGROUPRn    = GIC_DIST_BASE + 0x080 // Interrupt Group Registers (n = 0..31)
	GICD_ISENABLERn  = GIC_DIST_BASE + 0x100 // Interrupt Set-Enable Registers (n = 0..31)
	GICD_ICENABLERn  = GIC_DIST_BASE + 0x180 // Interrupt Clear-Enable Registers (n = 0..31)
	GICD_ISPENDRn    = GIC_DIST_BASE + 0x200 // Interrupt Set-Pending Registers (n = 0..31)
	GICD_ICPENDRn    = GIC_DIST_BASE + 0x280 // Interrupt Clear-Pending Registers (n = 0..31)
	GICD_ISACTIVERn  = GIC_DIST_BASE + 0x300 // Interrupt Set-Active Registers (n = 0..31)
	GICD_ICACTIVERn  = GIC_DIST_BASE + 0x380 // Interrupt Clear-Active Registers (n = 0..31)
	GICD_IPRIORITYRn = GIC_DIST_BASE + 0x400 // Interrupt Priority Registers (n = 0..254)
	GICD_ITARGETSRn  = GIC_DIST_BASE + 0x800 // Interrupt Target Registers (n = 0..254)
	GICD_ICFGRn      = GIC_DIST_BASE + 0xC00 // Interrupt Configuration Registers (n = 0..63)
	GICD_SGIR        = GIC_DIST_BASE + 0xF00 // Software Generated Interrupt Register

	// GIC CPU Interface base address
	GIC_CPU_BASE = 0x08010000

	// GIC CPU Interface register offsets
	GICC_CTLR   = GIC_CPU_BASE + 0x000 // CPU Interface Control Register
	GICC_PMR    = GIC_CPU_BASE + 0x004 // Interrupt Priority Mask Register
	GICC_BPR    = GIC_CPU_BASE + 0x008 // Binary Point Register
	GICC_IAR    = GIC_CPU_BASE + 0x00C // Interrupt Acknowledge Register
	GICC_EOIR   = GIC_CPU_BASE + 0x010 // End of Interrupt Register
	GICC_RPR    = GIC_CPU_BASE + 0x014 // Running Priority Register
	GICC_HPPIR  = GIC_CPU_BASE + 0x018 // Highest Pending Interrupt Register
	GICC_ABPR   = GIC_CPU_BASE + 0x01C // Aliased Binary Point Register
	GICC_AIAR   = GIC_CPU_BASE + 0x020 // Aliased Interrupt Acknowledge Register
	GICC_AEOIR  = GIC_CPU_BASE + 0x024 // Aliased End of Interrupt Register
	GICC_AHPPIR = GIC_CPU_BASE + 0x028 // Aliased Highest Pending Interrupt Register
	GICC_APR    = GIC_CPU_BASE + 0x0D0 // Active Priority Register
	GICC_NSAPR  = GIC_CPU_BASE + 0x0E0 // Non-secure Active Priority Register
	GICC_IIDR   = GIC_CPU_BASE + 0x0FC // CPU Interface Identification Register

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
	// Call the full initialization
	gicInitFull()
}

// gicInitFull is the original full initialization (kept for easy revert)
// To revert: replace gicInit() body with gicInitFull() body
//
//go:nosplit
func gicInitFull() {
	uartPuts("DEBUG: gicInit called\r\n")

	// Interrupts are already disabled during kernel init
	// Don't call disable_irqs() - it was causing hangs
	// disable_irqs()

	uartPuts("DEBUG: Starting GIC initialization steps...\r\n")

	// Step 1: Disable distributor
	uartPuts("DEBUG: Step 1 - Disable distributor\r\n")
	mmio_write(GICD_CTLR, 0)

	// Step 2: Disable CPU interface
	uartPuts("DEBUG: Step 2 - Disable CPU interface\r\n")
	mmio_write(GICC_CTLR, 0)

	// Step 3: Set priority mask to allow all interrupts (lowest priority = 0xFF)
	// Lower value = higher priority
	uartPuts("DEBUG: Step 3 - Set priority mask\r\n")
	mmio_write(GICC_PMR, 0xFF)

	// Step 4: Configure binary point register (BPR)
	// BPR = 0 means 4-bit priority grouping (no preemption)
	uartPuts("DEBUG: Step 4 - Configure BPR\r\n")
	mmio_write(GICC_BPR, 0)

	// Step 5: Clear all pending interrupts
	uartPuts("DEBUG: Step 5 - Clear pending interrupts\r\n")
	for i := 0; i < 32; i++ {
		mmio_write(GICD_ICPENDRn+uintptr(i*4), 0xFFFFFFFF)
	}

	// Step 6: Route all interrupts to Group 0 (secure)
	// CRITICAL: QEMU virt with GICv2 only works reliably with Group 0
	// Real Raspberry Pi 4 hardware may require Group 1 (Non-secure) - TO BE TESTED
	// Group 0 = secure interrupts (IRQ), Group 1 = non-secure (IRQ)
	// TODO: Add runtime detection or build-time flag for real hardware
	uartPuts("DEBUG: Step 6 - Set all interrupts to Group 0 (secure)\r\n")
	for i := 0; i < 32; i++ {
		mmio_write(GICD_IGROUPRn+uintptr(i*4), 0x00000000) // All in Group 0
	}

	// Step 7: Set interrupt priorities (default: 0x80 = medium priority)
	// Lower value = higher priority
	uartPuts("DEBUG: Step 7 - Set interrupt priorities\r\n")
	for i := 0; i < 256; i++ {
		mmio_write(GICD_IPRIORITYRn+uintptr(i*4), 0x80808080) // 4 interrupts per register
	}

	// Step 8: Route all interrupts to CPU 0
	// For PPIs (16-31), this is ignored, but we set it anyway
	uartPuts("DEBUG: Step 8 - Route interrupts to CPU 0\r\n")
	for i := 0; i < 256; i++ {
		mmio_write(GICD_ITARGETSRn+uintptr(i*4), 0x01010101) // CPU 0 = bit 0
	}

	// Step 9: Configure interrupts as level-triggered (default)
	// Bit 0 = 0 means level-triggered, 1 = edge-triggered
	// Timer interrupts are level-triggered
	uartPuts("DEBUG: Step 9 - Configure interrupt types\r\n")
	for i := 0; i < 64; i++ {
		mmio_write(GICD_ICFGRn+uintptr(i*4), 0) // Level-triggered
	}

	// Step 10: Enable distributor
	// Enable Group 0 only for QEMU virt compatibility
	// Bit 0 = Enable Group 0 (Secure)
	// Real hardware may need Group 1 instead - TO BE TESTED
	uartPuts("DEBUG: Step 10 - Enable distributor (Group 0 only)\r\n")
	mmio_write(GICD_CTLR, 0x01) // Enable Group 0 only

	// Step 11: Enable CPU interface
	// Enable Group 0 only for QEMU virt compatibility
	// Bit 0 = Enable Group 0 (Secure)
	// Real hardware may need Group 1 instead - TO BE TESTED
	uartPuts("DEBUG: Step 11 - Enable CPU interface (Group 0 only)\r\n")
	mmio_write(GICC_CTLR, 0x01) // Enable Group 0 only

	uartPuts("GIC initialized\r\n")
}

// checkSecurityState checks if we're running in Secure or Non-secure EL1
//
//go:nosplit
func checkSecurityState() {
	uartPuts("\r\n=== Security State Check ===\r\n")

	// Read ID_AA64PFR0_EL1 to check EL3 support
	pfr0 := read_id_aa64pfr0_el1()
	el3Support := (pfr0 >> 12) & 0xF

	uartPuts("EL3 support: ")
	if el3Support == 0 {
		uartPuts("NOT implemented - likely Non-secure\r\n")
	} else if el3Support == 1 {
		uartPuts("AArch64 only\r\n")
	} else if el3Support == 2 {
		uartPuts("AArch64 + AArch32\r\n")
	} else {
		uartPuts("unknown\r\n")
	}

	// Check GIC Group registers - read back what we wrote
	gicdIgroupr0 := mmio_read(GICD_IGROUPRn)
	uartPuts("GICD_IGROUPR0: ")
	if gicdIgroupr0 == 0x00000000 {
		uartPuts("All interrupts in Group 0\r\n")
	} else if gicdIgroupr0 == 0xFFFFFFFF {
		uartPuts("All interrupts in Group 1\r\n")
	} else {
		uartPuts("Mixed groups\r\n")
	}

	// Check GICD_CTLR
	gicdCtlr := mmio_read(GICD_CTLR)
	uartPuts("GICD_CTLR: ")
	if (gicdCtlr & 0x01) != 0 {
		uartPuts("Group 0 enabled")
	}
	if (gicdCtlr & 0x02) != 0 {
		if (gicdCtlr & 0x01) != 0 {
			uartPuts(", ")
		}
		uartPuts("Group 1 enabled")
	}
	uartPuts("\r\n")

	// Check GICC_CTLR
	giccCtlr := mmio_read(GICC_CTLR)
	uartPuts("GICC_CTLR: ")
	if (giccCtlr & 0x01) != 0 {
		uartPuts("Group 0 enabled")
	}
	if (giccCtlr & 0x02) != 0 {
		if (giccCtlr & 0x01) != 0 {
			uartPuts(", ")
		}
		uartPuts("Group 1 enabled")
	}
	uartPuts("\r\n")

	uartPuts("\r\nConclusion: ")
	if el3Support == 0 && gicdIgroupr0 == 0x00000000 {
		uartPuts("Likely QEMU allowing Group 0 in Non-secure mode\r\n")
	} else if el3Support != 0 && gicdIgroupr0 == 0x00000000 {
		uartPuts("Possibly running in Secure EL1\r\n")
	} else {
		uartPuts("Unknown configuration\r\n")
	}
	uartPuts("=== End Security Check ===\r\n\r\n")
}

// gicEnableInterrupt enables a specific interrupt in the GIC
//
//go:nosplit
func gicEnableInterrupt(irqID uint32) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}

	// Calculate register index (32 interrupts per register)
	regIndex := irqID / 32
	bitIndex := irqID % 32

	// Set enable bit in ISENABLER register
	mmio_write(GICD_ISENABLERn+uintptr(regIndex*4), 1<<bitIndex)
}

// gicDisableInterrupt disables a specific interrupt in the GIC
//
//go:nosplit
func gicDisableInterrupt(irqID uint32) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}

	// Calculate register index (32 interrupts per register)
	regIndex := irqID / 32
	bitIndex := irqID % 32

	// Clear enable bit in ICENABLER register
	mmio_write(GICD_ICENABLERn+uintptr(regIndex*4), 1<<bitIndex)
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
	// Read IAR (Interrupt Acknowledge Register)
	// Bits 9:0 = interrupt ID
	// Bit 10 = CPU ID (for SGIs)
	// 1023 = spurious interrupt
	iar := mmio_read(GICC_IAR)
	return iar & 0x3FF // Return interrupt ID (bits 9:0)
}

// gicEndOfInterrupt signals end of interrupt handling
//
//go:nosplit
func gicEndOfInterrupt(irqID uint32) {
	// Write interrupt ID to EOIR (End of Interrupt Register)
	mmio_write(GICC_EOIR, irqID)
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
	uartPutc('H')

	// Note: Interrupts should already be enabled before timer starts
	// Do NOT enable interrupts from inside the interrupt handler!

	// Acknowledge interrupt and get ID
	irqID := gicAcknowledgeInterrupt()
	uartPutc('A')

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
		uart_putc_pl011('U') // 'U' for Unhandled
		uart_putc_pl011('I') // 'I' for Interrupt
		uart_putc_pl011(':') // ':'
		uart_putc_pl011(' ')
		// Simple digit output for interrupt ID
		if irqID < 10 {
			uart_putc_pl011(byte('0' + irqID))
		} else {
			uart_putc_pl011(byte('0' + irqID/10))
			uart_putc_pl011(byte('0' + irqID%10))
		}
		uart_putc_pl011('\r')
		uart_putc_pl011('\n')
	}

	// Signal end of interrupt
	gicEndOfInterrupt(irqID)
}
