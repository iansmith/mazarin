// MAZZY USERSPACE OVERLAY: Wall clock time from ARM64 generic timer.
//
// This file is part of the userspace overlay and replaces the assembly
// walltime function from sys_linux_arm64.s. It computes real wall clock
// time using the boot epoch passed from the kernel via environment variables
// and the ARM64 CNTVCT_EL0 counter register.
//
// Before init() runs (during early runtime bootstrap), walltime returns
// monotonic time derived from the counter. After init(), it returns real
// Unix epoch time.

//go:build linux && arm64

package runtime

// nsPerTickX256 is the fixed-point (×256) nanoseconds per timer tick.
// Default 4096 = 16 ns/tick × 256 for ARM64 62.5 MHz timer.
// Updated by init() with the actual value from CNTFRQ_EL0.
// Referenced by assembly nanotime1 in sys_linux_arm64.s.
var nsPerTickX256 uint64 = 4096

// Boot epoch state — set from environment variables during init.
var (
	mazzyBootSec   int64  // Unix epoch seconds at boot time
	mazzyBootNsec  int64  // Sub-second nanoseconds at boot time
	mazzyBootTicks uint64 // CNTVCT_EL0 value when boot epoch was measured
	mazzyBootReady uint32 // Non-zero when boot epoch is initialized
	mazzyFreq      uint64 // Cached CNTFRQ_EL0 value (set in init)
)

// readCNTVCT reads the virtual counter register (CNTVCT_EL0).
// Implemented in sys_linux_arm64.s.
func readCNTVCT() uint64

// readCNTFRQ reads the counter frequency register (CNTFRQ_EL0).
// Implemented in sys_linux_arm64.s.
func readCNTFRQ() uint64

// walltime returns wall clock time derived from the ARM64 generic timer
// and boot epoch data passed from the kernel via environment variables.
// Before init(), returns monotonic time (sufficient for runtime bootstrap).
//
//go:nosplit
func walltime() (sec int64, nsec int32) {
	ticks := readCNTVCT()

	if mazzyBootReady == 0 {
		// Before init: approximate using nsPerTickX256
		totalNs := (ticks * nsPerTickX256) >> 8
		return int64(totalNs / 1000000000), int32(totalNs % 1000000000)
	}

	elapsed := ticks - mazzyBootTicks
	elapsedSec := elapsed / mazzyFreq
	remainder := elapsed % mazzyFreq
	elapsedNsec := (remainder * 1000000000) / mazzyFreq

	totalNsec := uint64(mazzyBootNsec) + elapsedNsec
	extraSec := totalNsec / 1000000000
	totalNsec = totalNsec % 1000000000

	return mazzyBootSec + int64(elapsedSec) + int64(extraSec), int32(totalNsec)
}

func init() {
	// Cache counter frequency and set nsPerTickX256 from it
	mazzyFreq = readCNTFRQ()
	if mazzyFreq > 0 {
		nsPerTickX256 = 256000000000 / mazzyFreq
	}

	// Parse boot epoch from environment variables
	bootSec := gogetenv("MAZZY_BOOT_SEC")
	bootTicks := gogetenv("MAZZY_BOOT_TICKS")
	bootNsec := gogetenv("MAZZY_BOOT_NSEC")

	if bootSec != "" && bootTicks != "" {
		mazzyBootSec = mazzyAtoi64(bootSec)
		mazzyBootTicks = uint64(mazzyAtoi64(bootTicks))
		if bootNsec != "" {
			mazzyBootNsec = mazzyAtoi64(bootNsec)
		}
		mazzyBootReady = 1
	}
}

// mazzyAtoi64 parses a decimal string to int64. Returns 0 on invalid input.
func mazzyAtoi64(s string) int64 {
	if len(s) == 0 {
		return 0
	}
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	var n int64
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		return -n
	}
	return n
}
