//go:build qemuvirt && aarch64

package main

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
	IRQ_ID_TIMER_PPI = 27 // ARM Generic Timer PPI - Virtual Timer (CNTV) for EL1 - ID 27 (like reference repo)
)

// Interrupt handler function type
type InterruptHandler func()

var (
	// Array of interrupt handlers (indexed by interrupt ID)
	// We support up to 1020 interrupts (0-1019)
	interruptHandlers [1020]InterruptHandler
)

// gicInit initializes the Generic Interrupt Controller
//
//go:nosplit
func gicInit() {
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

	// Step 6: Route all interrupts to Group 1NS so they signal as IRQs
	// Group 0 delivers as FIQ when running non-secure EL1, and we never clear the F-bit.
	// Leaving Group 0 here would prevent timer PPIs (27) from being delivered.
	uartPuts("DEBUG: Step 6 - Set interrupt groups to Group1NS\r\n")
	for i := 0; i < 32; i++ {
		mmio_write(GICD_IGROUPRn+uintptr(i*4), 0xFFFFFFFF)
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
	// Enable both Group 0 and Group 1 interrupts
	// Bit 0 = Enable Group 0, Bit 1 = Enable Group 1
	uartPuts("DEBUG: Step 10 - Enable distributor (Groups 0 and 1)\r\n")
	mmio_write(GICD_CTLR, 0x03) // Enable both groups (0x03 = bits 0 and 1)

	// Step 11: Enable CPU interface
	// Enable both Group 0 and Group 1 interrupts in CPU interface
	// Bit 0 = Enable Group 0, Bit 1 = Enable Group 1 (non-secure)
	uartPuts("DEBUG: Step 11 - Enable CPU interface (Groups 0 and 1)\r\n")
	mmio_write(GICC_CTLR, 0x03) // Enable both groups

	uartPuts("GIC initialized\r\n")
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
//go:nosplit
func registerInterruptHandler(irqID uint32, handler InterruptHandler) {
	if irqID >= 1020 {
		return // Invalid interrupt ID
	}
	uartPuts("DEBUG: registerInterruptHandler - about to assign to array\r\n")
	interruptHandlers[irqID] = handler
	uartPuts("DEBUG: registerInterruptHandler - assignment complete\r\n")
}

var interruptsEnabled bool

// gicHandleInterrupt handles an interrupt from the GIC
// This is called from IRQHandler and from assembly exception handlers
//
//go:nosplit
//go:noinline
func gicHandleInterrupt() {
	// On first interrupt, enable interrupts from assembly
	// This avoids issues with Go runtime triggering exceptions
	if !interruptsEnabled {
		// Enable interrupts from pure assembly - this is safe in interrupt context
		enable_irqs_asm()
		interruptsEnabled = true
		uartPuts("DEBUG: Interrupts enabled on first IRQ\r\n")
	}

	// Acknowledge interrupt and get ID
	irqID := gicAcknowledgeInterrupt()

	// Check for spurious interrupt (ID 1023)
	if irqID >= 1020 {
		return // Spurious interrupt, ignore
	}

	// Call registered handler if available
	if interruptHandlers[irqID] != nil {
		// Call handler
		interruptHandlers[irqID]()
	} else {
		// No handler registered - log it
		uartPuts("Unhandled interrupt: ")
		uartPutUint32(irqID)
		uartPuts("\r\n")
	}

	// Signal end of interrupt
	gicEndOfInterrupt(irqID)
}
