//go:build test_stubs && arm64

package main

// ARM64-specific stub implementations for test builds.
// These match the declarations in asm_decl_arm64.go.

//go:nosplit
func getAuxval(tag uint64) uint64 { return 0 }

//go:nosplit
func EnableGIC() {}

//go:nosplit
func DisableTimerHardware() {}

//go:nosplit
func RearmTimerNow() {}

//go:nosplit
func ReadCntvCtlEl0() uint64 { return 0 }

//go:nosplit
func ReadCntvTvalEl0() uint64 { return 0 }

//go:nosplit
func ReadCntvctEl0() uint64 { return 1000 }

//go:nosplit
func ReadCntfrqEl0() uint64 { return 62500000 }

//go:nosplit
func ReadDAIF() uint64 { return 0 }
