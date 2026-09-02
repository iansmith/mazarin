//go:build arm64

package main

// ARM64-specific assembly function declarations.
// Implementations are in gic_arm64.s and runtime_arm64.s.

//go:nosplit
func getAuxval(tag uint64) uint64

// getExceptionVectorBlobEnd returns the address of the zero-body marker
// placed immediately after the exception-vector blob in exceptions_arm64.s.
// Paired with GetExceptionVectorBase by initResumeGuardBounds (MAZ-196).
//
//go:nosplit
func getExceptionVectorBlobEnd() uintptr

//go:nosplit
func EnableGIC()

// DisableTimerHardware disables the ARM timer hardware (CNTV_CTL_EL0).
// This stops the timer from generating interrupt requests.
//
//go:nosplit
func DisableTimerHardware()

//go:nosplit
func RearmTimerNow()

// Timer register read functions
//
//go:nosplit
func ReadCntvCtlEl0() uint64

//go:nosplit
func ReadCntvTvalEl0() uint64

//go:nosplit
func ReadCntvctEl0() uint64

//go:nosplit
func ReadCntfrqEl0() uint64
