// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// MAZZY USERSPACE OVERLAY: wall-clock time from the ARM64 generic timer.
//
// This file overlays stock Go's `runtime/timestub2.go`. The stock file
// provides a wasmimport forward declaration of `walltime()` on platforms
// that don't have an asm impl. On linux/arm64 the stock asm impl in
// `sys_linux_arm64.s` was removed by the overlay companion file in this
// directory, so we provide a Go implementation here instead — reading
// CNTVCT_EL0 directly and applying a boot epoch published by kmazarin via
// environment variables (MAZZY_BOOT_SEC / MAZZY_BOOT_TICKS / MAZZY_BOOT_NSEC).
//
// On platforms other than linux/arm64 this file is excluded by build tag
// and the relevant native stock impl is used (e.g. `sys_linux_amd64.s`).
// Mazzy only builds for linux/arm64 + linux/amd64; non-arm64 non-amd64
// linuxes that relied on the stock forward declaration are out of scope.
//
// MAZ-46 Phase 2: this file consolidates what used to be two overlay
// files (timestub2.go + walltime_mazzy.go). Three deltas vs the stock
// timestub2.go: build tag narrowed to `linux && arm64`, wasmimport decl
// dropped, full impl + boot-epoch init added.

//go:build linux && arm64

package runtime

// nsPerTickX256 is the fixed-point (×256) nanoseconds per timer tick.
// Default 4096 = 16 ns/tick × 256 for an ARM64 62.5 MHz timer.
// Updated by init() with the actual value derived from CNTFRQ_EL0.
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
// Before init() runs (during early runtime bootstrap), returns monotonic
// time approximated from the counter — sufficient for runtime bootstrap.
//
//go:nosplit
func walltime() (sec int64, nsec int32) {
	ticks := readCNTVCT()

	if mazzyBootReady == 0 || mazzyFreq == 0 {
		// Before init, or hardware reported a 0 frequency (would div-by-zero
		// below): approximate using nsPerTickX256's default.
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

	// Require mazzyFreq > 0 too — walltime's post-init branch divides by it,
	// so a 0 frequency would cause an integer-divide panic. Falls through to
	// the pre-init branch (which uses nsPerTickX256) when freq is 0.
	if bootSec != "" && bootTicks != "" && mazzyFreq > 0 {
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
