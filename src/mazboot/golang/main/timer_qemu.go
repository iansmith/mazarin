//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"

	_ "unsafe"
)

// Required for //go:linkname directives

// ARM Generic Timer for QEMU virt machine
// The ARM Generic Timer is integrated into each CPU core
//
// EXPERIMENT: Testing PHYSICAL timer (CNTP_*) vs VIRTUAL timer (CNTV_*)
// Set USE_PHYSICAL_TIMER = true to test physical timer (PPI 30)
// Set USE_PHYSICAL_TIMER = false to revert to virtual timer (PPI 27)
//
// Timer interrupts are PPIs (Private Peripheral Interrupts) routed through GIC
// Virtual Timer PPI ID: 27
// Physical Timer PPI ID: 30

// SWITCH: Set to true for physical timer, false for virtual timer
const USE_PHYSICAL_TIMER = false // Use virtual timer (matches working commit)

// System register access functions (must be implemented in assembly)
// VIRTUAL timer functions (CNTV_*)
//
//go:linkname read_cntv_ctl_el0 read_cntv_ctl_el0
//go:nosplit
func read_cntv_ctl_el0() uint32

//go:linkname write_cntv_ctl_el0 write_cntv_ctl_el0
//go:nosplit
func write_cntv_ctl_el0(value uint32)

//go:linkname read_cntv_tval_el0 read_cntv_tval_el0
//go:nosplit
func read_cntv_tval_el0() uint32

//go:linkname write_cntv_tval_el0 write_cntv_tval_el0
//go:nosplit
func write_cntv_tval_el0(value uint32)

//go:linkname read_cntv_cval_el0 read_cntv_cval_el0
//go:nosplit
func read_cntv_cval_el0() uint64

//go:linkname write_cntv_cval_el0 write_cntv_cval_el0
//go:nosplit
func write_cntv_cval_el0(value uint64)

//go:linkname read_cntvct_el0 read_cntvct_el0
//go:nosplit
func read_cntvct_el0() uint64

// PHYSICAL timer functions (CNTP_*)
//
//go:linkname read_cntp_ctl_el0 read_cntp_ctl_el0
//go:nosplit
func read_cntp_ctl_el0() uint32

//go:linkname write_cntp_ctl_el0 write_cntp_ctl_el0
//go:nosplit
func write_cntp_ctl_el0(value uint32)

//go:linkname read_cntp_tval_el0 read_cntp_tval_el0
//go:nosplit
func read_cntp_tval_el0() uint32

//go:linkname write_cntp_tval_el0 write_cntp_tval_el0
//go:nosplit
func write_cntp_tval_el0(value uint32)

//go:linkname read_cntp_cval_el0 read_cntp_cval_el0
//go:nosplit
func read_cntp_cval_el0() uint64

//go:linkname write_cntp_cval_el0 write_cntp_cval_el0
//go:nosplit
func write_cntp_cval_el0(value uint64)

//go:linkname read_cntpct_el0 read_cntpct_el0
//go:nosplit
func read_cntpct_el0() uint64

//go:linkname read_cntfrq_el0 read_cntfrq_el0
//go:nosplit
func read_cntfrq_el0() uint32

// Timer control register bits (same for physical and virtual timers)
const (
	CNT_CTL_ENABLE  = 1 << 0 // Enable timer
	CNT_CTL_IMASK   = 1 << 1 // Interrupt mask (1 = masked)
	CNT_CTL_ISTATUS = 1 << 2 // Interrupt status (1 = pending)

	// Legacy names for compatibility
	CNTV_CTL_ENABLE  = CNT_CTL_ENABLE
	CNTV_CTL_IMASK   = CNT_CTL_IMASK
	CNTV_CTL_ISTATUS = CNT_CTL_ISTATUS
)

var (
	timerInitialized bool
	timerTicks       uint64 // Number of timer ticks since initialization
	timerExitCount   uint32 // Countdown to exit (decremented on each timer interrupt)
)

// Timer wrapper functions that call physical or virtual timer based on USE_PHYSICAL_TIMER
//
//go:nosplit
func timer_read_ctl() uint32 {
	if USE_PHYSICAL_TIMER {
		return read_cntp_ctl_el0()
	}
	return read_cntv_ctl_el0()
}

//go:nosplit
func timer_write_ctl(value uint32) {
	if USE_PHYSICAL_TIMER {
		write_cntp_ctl_el0(value)
	} else {
		write_cntv_ctl_el0(value)
	}
}

//go:nosplit
func timer_read_tval() uint32 {
	if USE_PHYSICAL_TIMER {
		return read_cntp_tval_el0()
	}
	return read_cntv_tval_el0()
}

//go:nosplit
func timer_write_tval(value uint32) {
	if USE_PHYSICAL_TIMER {
		write_cntp_tval_el0(value)
	} else {
		write_cntv_tval_el0(value)
	}
}

// Get timer name for debug output
//
//go:nosplit
func timer_name() string {
	if USE_PHYSICAL_TIMER {
		return "PHYSICAL"
	}
	return "VIRTUAL"
}

// Get timer PPI ID
//
//go:nosplit
func timer_irq_id() uint32 {
	if USE_PHYSICAL_TIMER {
		return 30 // Physical timer PPI
	}
	return 27 // Virtual timer PPI
}

// timerInit initializes the ARM Generic Timer
// Supports both physical and virtual timer via USE_PHYSICAL_TIMER constant
//
//go:nosplit
func timerInit() {
	if timerInitialized {
		return
	}

	// QEMU virt machine uses 62.5 MHz timer frequency
	freq := uint32(62500000)

	// Disable timer first (clears any pending interrupts)
	timer_write_ctl(0)

	// Set TVAL for 5 second countdown (312500000 ticks at 62.5MHz)
	timer_write_tval(freq * 5)

	// Enable timer with interrupts unmasked
	timer_write_ctl(CNT_CTL_ENABLE)

	// Register timer interrupt handler with GIC
	irqId := timer_irq_id()
	registerInterruptHandler(irqId, timerInterruptHandler)
	gicEnableInterrupt(irqId)

	timerInitialized = true
	timerExitCount = 5 // Exit after 5 timer interrupts (25 seconds total)
}

// checkTimerStatus checks if timer interrupt is pending (debugging only)
//
//go:nosplit
func checkTimerStatus() {
	// Not used during normal operation - kept for debugging
}

// timerInterruptHandler is a legacy handler - timer interrupts are now handled
// entirely in assembly (src/asm/exceptions.s). This function is kept for
// reference but is not called.
//
//go:nosplit
func timerInterruptHandler() {
	// Increment tick counter
	timerTicks++

	// Print 'T' to show timer interrupt fired
	printChar('T')

	// Decrement exit counter
	if timerExitCount > 0 {
		timerExitCount--
		if timerExitCount == 0 {
			// Exit after 5 seconds (5 timer interrupts at 1 second each)
			print("Timer: 5 seconds elapsed, exiting via semihosting...\r\n")
			asm.QemuExit()
			return
		}
	}

	// Reset timer to fire again in 5 seconds
	// Use TVAL (timer value - counts down)
	freq := uint64(62500000)           // Default QEMU virt timer frequency = 62.5MHz
	timer_write_tval(uint32(freq * 5)) // Set countdown timer for 5 seconds

	// Output '.' to framebuffer
	fb_putc_irq('.')
}

// timerInterruptHandlerAsm is called directly from assembly after time-critical
// operations (IAR read, timer reset, EOIR write) are complete
// This allows developers to write handler logic in Go while keeping
// time-critical GIC operations in assembly
//
//go:linkname timerInterruptHandlerAsm timerInterruptHandlerAsm
//go:nosplit
//go:noinline
func timerInterruptHandlerAsm(irqID uint32) {
	// irqID already acknowledged and EOI already sent by assembly
	// Just do non-time-critical work here

	if irqID == 27 { // Virtual timer
		// Increment tick counter
		timerTicks++

		// Decrement exit counter
		if timerExitCount > 0 {
			timerExitCount--
			if timerExitCount == 0 {
				// Exit after 5 seconds
				print("\r\nTimer: 5 seconds elapsed, exiting...\r\n")
				asm.QemuExit()
				return
			}
		}

		// Output '.' to framebuffer (non-time-critical)
		fb_putc_irq('.')
	}
}

// timerSet sets the timer to fire after a specified number of microseconds
// This is a helper function for setting custom timer intervals
//
//go:nosplit
func timerSet(usec uint32) {
	if !timerInitialized {
		return
	}

	// Read timer frequency
	freq := read_cntfrq_el0()

	// Calculate ticks: usec * freq / 1000000
	// Use 64-bit arithmetic to avoid overflow
	ticks := (uint64(usec) * uint64(freq)) / 1000000

	// Set timer value (TVAL counts down)
	if ticks > 0xFFFFFFFF {
		ticks = 0xFFFFFFFF // Clamp to 32-bit
	}
	write_cntv_tval_el0(uint32(ticks))
}
