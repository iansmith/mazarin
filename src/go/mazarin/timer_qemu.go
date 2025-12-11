//go:build qemuvirt && aarch64

package main

import (
	_ "unsafe" // Required for //go:linkname directives
)

// ARM Generic Timer for QEMU virt machine
// The ARM Generic Timer is integrated into each CPU core
// IMPORTANT: Using VIRTUAL timer (CNTV_*) at EL1 - matches reference repo!
// Virtual timer is the correct choice for EL1 OS/kernel
// Timer interrupts are PPIs (Private Peripheral Interrupts) routed through GIC
// Virtual Timer PPI ID: 27 (Physical Timer is ID 30, used at EL2)

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

//go:linkname read_cntfrq_el0 read_cntfrq_el0
//go:nosplit
func read_cntfrq_el0() uint32

// Timer control register bits (same for physical and virtual timers)
const (
	CNTV_CTL_ENABLE  = 1 << 0 // Enable timer
	CNTV_CTL_IMASK   = 1 << 1 // Interrupt mask (1 = masked)
	CNTV_CTL_ISTATUS = 1 << 2 // Interrupt status (1 = pending)
)

var (
	timerInitialized bool
	timerTicks       uint64 // Number of timer ticks since initialization
	timerExitCount   uint32 // Countdown to exit (decremented on each timer interrupt)
)

// timerInit initializes the ARM Generic Timer
// freqHz: Timer frequency in Hz (typically 62.5 MHz for QEMU)
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

	uartPuts("DEBUG: About to disable virtual timer...\r\n")
	// Disable timer first (this clears any pending interrupts)
	write_cntv_ctl_el0(0)
	uartPuts("DEBUG: Virtual timer disabled\r\n")

	uartPuts("DEBUG: About to set virtual timer value (TVAL)...\r\n")
	// Use TVAL (timer value - counts down) - simpler and more direct!
	// TVAL is a 32-bit countdown timer, fires when reaches 0
	// Set to 62500000 ticks = 1 second at 62.5MHz (will fire every second)
	timerValue := freq * 1 // 62500000 (1 second)
	write_cntv_tval_el0(uint32(timerValue))
	uartPuts("DEBUG: Virtual timer TVAL set (1 second countdown, will exit after 5 interrupts)\r\n")

	uartPuts("DEBUG: About to enable virtual timer...\r\n")
	// Enable timer with interrupts unmasked
	// CNTV_CTL_ENABLE = 1 (bit 0), CNTV_CTL_IMASK = 0 (bit 1 cleared = interrupts enabled)
	write_cntv_ctl_el0(CNTV_CTL_ENABLE)

	// Verify timer control register was set correctly
	ctl := read_cntv_ctl_el0()
	uartPuts("DEBUG: CNTV_CTL_EL0 after enable = 0x")
	uartPutHex8(uint8(ctl))
	uartPuts(" (ENABLE=")
	if (ctl & CNTV_CTL_ENABLE) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(", IMASK=")
	if (ctl & CNTV_CTL_IMASK) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(", ISTATUS=")
	if (ctl & CNTV_CTL_ISTATUS) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(")\r\n")
	uartPuts("DEBUG: Virtual timer enabled (ENABLE=1, IMASK=0)\r\n")

	uartPuts("DEBUG: Enabling timer interrupt in GIC...\r\n")
	// Note: Timer interrupt is handled by assembly handler (handle_timer_irq)
	// which calls the Go handleTimerIRQ function (nosplit, minimal)
	// Enable timer interrupt in GIC
	gicEnableInterrupt(IRQ_ID_TIMER_PPI)
	uartPuts("DEBUG: Timer interrupt enabled in GIC\r\n")

	// Read timer value to verify it was set (but don't print - uartPutUint32 causes alignment issues in interrupt context)
	// tval := read_cntv_tval_el0()
	// uartPuts("DEBUG: CNTV_TVAL_EL0 = ")
	// uartPutUint32(tval)  // REMOVED: Causes alignment fault - creates unaligned store at [sp, #53]
	// uartPuts(" (should be 62500000)\r\n")

	timerInitialized = true
	timerExitCount = 5 // Exit after 5 timer interrupts (5 seconds)
	uartPuts("Timer initialized (will exit after 5 seconds)\r\n")

	// Note: enable_irqs() hangs - msr DAIFCLR causes sync exception
	// Interrupts will be enabled from pure assembly after initialization
	// For now, timer is configured and ready - interrupts just need to be enabled
}

// handleTimerIRQ is called from assembly interrupt handler
// This is a minimal nosplit function that handles timer interrupts
//
//go:linkname handleTimerIRQ handleTimerIRQ
//go:nosplit
//go:noinline
func handleTimerIRQ() {
	// Decrement exit counter
	if timerExitCount > 0 {
		timerExitCount--
		if timerExitCount == 0 {
			// Exit after 5 seconds (5 timer interrupts)
			uartPuts("Timer: 5 seconds elapsed, exiting via semihosting...\r\n")
			qemu_exit()
			return
		}
	}

	// Reset timer to fire again in 1 second
	// Use TVAL (timer value - counts down)
	freq := uint64(62500000)              // Default QEMU virt timer frequency = 62.5MHz
	write_cntv_tval_el0(uint32(freq * 1)) // Set countdown timer for 1 second

	// Output '.' to framebuffer
	fb_putc_irq('.')
}

// timerInterruptHandler is the old handler - kept for reference but not used
// The assembly handler calls handleTimerIRQ directly instead
//
//go:nosplit
func timerInterruptHandler() {
	// Increment tick counter
	timerTicks++

	// Print 'T' to show timer interrupt fired
	uartPutc('T')

	// Reset timer to fire again in 5 seconds
	// Use TVAL (timer value - counts down)
	freq := uint64(62500000)              // Default QEMU virt timer frequency = 62.5MHz
	write_cntv_tval_el0(uint32(freq * 5)) // Set countdown timer for 5 seconds
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
