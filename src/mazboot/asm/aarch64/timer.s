// Timer functions for ARM Generic Timer
// Provides access to CNTVCT_EL0 (counter) and CNTFRQ_EL0 (frequency) registers

.text

// readTimerCounter() uint64
//
// Reads the ARM Generic Timer virtual counter (CNTVCT_EL0).
// This returns the current hardware tick count.
// At 62.5 MHz, this increments 62,500,000 times per second.
//
// Returns: uint64 hardware tick count (in x0)
.global readTimerCounter
.type readTimerCounter, %function
readTimerCounter:
    mrs x0, CNTVCT_EL0
    ret

// getTimerFrequency() uint64
//
// Reads the ARM Generic Timer frequency register (CNTFRQ_EL0).
// This returns the timer frequency in Hz (e.g., 62,500,000 for 62.5 MHz).
//
// Returns: uint64 frequency in Hz (in x0)
.global getTimerFrequency
.type getTimerFrequency, %function
getTimerFrequency:
    mrs x0, CNTFRQ_EL0
    ret

// armTimer() - Arms and enables the timer to fire first interrupt
//
// Sets CNTV_CVAL_EL0 (compare value) and enables the timer via CNTV_CTL_EL0.
// The timer will fire the first interrupt after 20ms (1,250,000 ticks @ 62.5MHz).
.global armTimer
.type armTimer, %function
armTimer:
    // Read current counter value
    mrs x0, CNTVCT_EL0

    // Add 1,250,000 ticks (20ms @ 62.5 MHz)
    movz x1, #0x0013, lsl #16       // 1250000 = 0x1312D0
    movk x1, #0x12D0, lsl #0
    add x0, x0, x1

    // Set compare value to trigger first interrupt
    msr CNTV_CVAL_EL0, x0

    // Enable timer: set bit 0 (ENABLE) and clear bit 1 (IMASK - unmask interrupt)
    mov x0, #1                      // ENABLE = 1, IMASK = 0
    msr CNTV_CTL_EL0, x0

    ret
