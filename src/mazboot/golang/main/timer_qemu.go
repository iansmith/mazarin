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

	// Read timer frequency (CNTFRQ_EL0)
	// TEMPORARY: Reading CNTFRQ_EL0 causes sync exception - use default
	// QEMU virt machine typically uses 62.5 MHz = 62500000 Hz
	freq := uint32(62500000) // Default QEMU virt timer frequency
	uartPuts("Timer frequency: 62500000 Hz (default)\r\n")

	// Show which timer we're using
	uartPuts("DEBUG: Using ")
	uartPuts(timer_name())
	uartPuts(" timer (PPI ")
	if USE_PHYSICAL_TIMER {
		uartPuts("30")
	} else {
		uartPuts("27")
	}
	uartPuts(")\r\n")

	uartPuts("DEBUG: About to disable timer...\r\n")
	// Disable timer first (this clears any pending interrupts)
	timer_write_ctl(0)
	uartPuts("DEBUG: Timer disabled\r\n")

	uartPuts("DEBUG: About to set timer value (TVAL)...\r\n")
	// Use TVAL (timer value - counts down) - simpler and more direct!
	// TVAL is a 32-bit countdown timer, fires when reaches 0
	// Set to 62500000 ticks = 1 second at 62.5MHz (will fire every second)
	timerValue := freq * 1 // 62500000 (1 second)
	timer_write_tval(uint32(timerValue))
	uartPuts("DEBUG: Timer TVAL set (1 second countdown)\r\n")

	uartPuts("DEBUG: About to enable timer...\r\n")
	// Enable timer with interrupts unmasked
	// CNT_CTL_ENABLE = 1 (bit 0), CNT_CTL_IMASK = 0 (bit 1 cleared = interrupts enabled)
	timer_write_ctl(CNT_CTL_ENABLE)

	// Verify timer control register was set correctly
	ctl := timer_read_ctl()
	uartPuts("DEBUG: Timer CTL after enable = 0x")
	uartPutHex8(uint8(ctl))
	uartPuts(" (ENABLE=")
	if (ctl & CNT_CTL_ENABLE) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(", IMASK=")
	if (ctl & CNT_CTL_IMASK) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(", ISTATUS=")
	if (ctl & CNT_CTL_ISTATUS) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(")\r\n")
	uartPuts("DEBUG: Timer enabled (ENABLE=1, IMASK=0)\r\n")

	uartPuts("DEBUG: Registering timer interrupt handler...\r\n")
	// Register timer interrupt handler with GIC (use correct PPI based on timer type)
	irqId := timer_irq_id()
	registerInterruptHandler(irqId, timerInterruptHandler)
	uartPuts("DEBUG: Handler registered\r\n")

	uartPuts("DEBUG: Enabling timer interrupt in GIC...\r\n")
	// Enable timer interrupt in GIC
	gicEnableInterrupt(irqId)
	uartPuts("DEBUG: Timer interrupt enabled in GIC\r\n")

	timerInitialized = true
	timerExitCount = 5 // Exit after 5 timer interrupts (5 seconds total)
	uartPuts("Timer initialized (will exit after 5 seconds)\r\n")

	// Verify timer interrupt is enabled in GIC
	uartPuts("DEBUG: Verifying timer interrupt enable in GIC...\r\n")
	// Read GICD_ISENABLER0 (interrupts 0-31 enable register)
	isenabler0 := asm.MmioRead(0x08000100) // GICD_ISENABLER0
	uartPuts("DEBUG: GICD_ISENABLER0 = 0x")
	uartPutHex32(isenabler0)
	uartPuts("\r\n")
	if (isenabler0 & (1 << irqId)) != 0 {
		uartPuts("DEBUG: Timer interrupt (")
		if USE_PHYSICAL_TIMER {
			uartPuts("30")
		} else {
			uartPuts("27")
		}
		uartPuts(") is ENABLED in GIC\r\n")
	} else {
		uartPuts("DEBUG: ERROR: Timer interrupt (")
		if USE_PHYSICAL_TIMER {
			uartPuts("30")
		} else {
			uartPuts("27")
		}
		uartPuts(") is NOT enabled in GIC!\r\n")
	}

	// Check timer value to see if it's counting down
	// Read TVAL multiple times to see if it's decreasing
	uartPuts("DEBUG: Checking if timer is counting down...\r\n")
	tval1 := timer_read_tval()
	uartPuts("DEBUG: Timer TVAL (first read) = ")
	uartPutHex32(tval1)
	uartPuts("\r\n")

	// Wait a bit (busy wait) and read again
	asm.BusyWait(1000000) // Wait ~1ms worth of cycles

	tval2 := timer_read_tval()
	uartPuts("DEBUG: Timer TVAL (after delay) = ")
	uartPutHex32(tval2)
	uartPuts("\r\n")

	if tval2 < tval1 {
		uartPuts("DEBUG: Timer IS counting down (decreased from ")
		uartPutHex32(tval1)
		uartPuts(" to ")
		uartPutHex32(tval2)
		uartPuts(")\r\n")
	} else if tval2 == tval1 {
		uartPuts("DEBUG: WARNING: Timer is NOT counting down (value unchanged: ")
		uartPutHex32(tval1)
		uartPuts(")\r\n")
	} else {
		uartPuts("DEBUG: ERROR: Timer value increased (should count down)! ")
		uartPutHex32(tval1)
		uartPuts(" -> ")
		uartPutHex32(tval2)
		uartPuts("\r\n")
	}

	// Note: enable_irqs() hangs - msr DAIFCLR causes sync exception
	// Interrupts will be enabled from pure assembly after initialization
	// For now, timer is configured and ready - interrupts just need to be enabled
}

// checkTimerStatus checks if timer interrupt is pending and prints diagnostic info
// This function is for debugging only - it's not called during normal operation
//
//go:nosplit
func checkTimerStatus() {
	// Check timer ISTATUS bit
	ctl := timer_read_ctl()
	istatus := (ctl & CNT_CTL_ISTATUS) != 0
	uartPuts("DEBUG: Timer status check - ISTATUS=")
	if istatus {
		uartPuts("1 (PENDING)")
	} else {
		uartPuts("0 (not pending)")
	}
	uartPuts(", ENABLE=")
	if (ctl & CNT_CTL_ENABLE) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(", IMASK=")
	if (ctl & CNT_CTL_IMASK) != 0 {
		uartPuts("1 (masked)")
	} else {
		uartPuts("0 (unmasked)")
	}
	uartPuts("\r\n")

	// Check GIC pending status for timer interrupt
	irqId := timer_irq_id()
	ispendr0 := asm.MmioRead(0x08000200) // GICD_ISPENDR0
	uartPuts("DEBUG: GICD_ISPENDR0 = 0x")
	uartPutHex32(ispendr0)
	uartPuts(" (bit ")
	if USE_PHYSICAL_TIMER {
		uartPuts("30")
	} else {
		uartPuts("27")
	}
	uartPuts(" = ")
	if (ispendr0 & (1 << irqId)) != 0 {
		uartPuts("1 = PENDING")
	} else {
		uartPuts("0 = not pending")
	}
	uartPuts(")\r\n")

	// Check GIC highest pending interrupt (HPPIR)
	hppir := asm.MmioRead(0x08010018) // GICC_HPPIR
	uartPuts("DEBUG: GICC_HPPIR = 0x")
	uartPutHex32(hppir)
	uartPuts(" (interrupt ID: ")
	uartPutHex32(hppir & 0x3FF)
	uartPuts(")\r\n")
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
	uartPutc('T')

	// Decrement exit counter
	if timerExitCount > 0 {
		timerExitCount--
		if timerExitCount == 0 {
			// Exit after 5 seconds (5 timer interrupts at 1 second each)
			uartPuts("Timer: 5 seconds elapsed, exiting via semihosting...\r\n")
			asm.QemuExit()
			return
		}
	}

	// Reset timer to fire again in 1 second
	// Use TVAL (timer value - counts down)
	freq := uint64(62500000)           // Default QEMU virt timer frequency = 62.5MHz
	timer_write_tval(uint32(freq * 1)) // Set countdown timer for 1 second

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
				uartPuts("\r\nTimer: 5 seconds elapsed, exiting...\r\n")
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
