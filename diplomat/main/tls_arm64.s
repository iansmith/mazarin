//go:build arm64

// diplomat/main/tls_arm64.s
// TLS (Thread-Local Storage) setup for ARM64
//
// On ARM64, TLS is accessed via the TPIDR_EL0 system register.
// Go's runtime expects to find g at [TPIDR_EL0, #-8].

#include "textflag.h"

// setupTLS sets TPIDR_EL0 for Go TLS support
// Go: func setupTLS(tlsAddr uintptr)
//
// After this call, loading from [TPIDR_EL0, #-8] will return
// the value stored at tlsAddr-8 (which should be the g pointer).
TEXT ·setupTLS(SB), NOSPLIT, $0-8
	MOVD	tlsAddr+0(FP), R0
	MSR	R0, TPIDR_EL0
	RET

// debugPortOut is a no-op on ARM64 UEFI
// (x86_64 uses port 0xE9 for QEMU debug, ARM64 doesn't have this)
// Go: func debugPortOut(c byte)
TEXT ·debugPortOut(SB), NOSPLIT, $0-1
	// No-op on ARM64 - use UEFI console for output
	RET
