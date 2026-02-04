//go:build riscv64 && !test_stubs

package ktimer

// RISC-V timer uses:
//   TIME CSR (0xC01)  - monotonic counter value (rdtime instruction)
//   SBI set_timer     - SBI call to set next timer interrupt deadline
//
// Timer interrupt is supervisor timer interrupt (cause 5).
// Frequency is obtained from device tree /cpus/timebase-frequency.
// QEMU virt default: 10MHz.

const platformTimerIRQ = 5

// PlatformDisableTimer clears the supervisor timer interrupt enable bit in SIE.
// Implemented in platform_riscv64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the supervisor timer interrupt number (cause 5).
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit returns the timer frequency in Hz.
// TODO: Read from device tree /cpus/timebase-frequency.
// For now, hardcode QEMU virt default (10MHz).
//
//go:nosplit
func PlatformTimerInit() uint32 {
	// QEMU virt machine default timebase-frequency
	return 10000000 // 10 MHz
}

// PlatformReadCounter reads the TIME CSR and returns the current counter value.
// Implemented in platform_riscv64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer sets the next timer interrupt at current + ticks.
// Uses SBI set_timer call with absolute deadline value.
// Implemented in platform_riscv64.s.
func PlatformRearmTimer(ticks uint64)
