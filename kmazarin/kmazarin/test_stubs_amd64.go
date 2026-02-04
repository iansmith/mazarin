//go:build test_stubs && amd64

package main

// x86_64-specific stub implementations for test builds.
// These match the declarations in asm_decl_amd64.go.

//go:nosplit
func ReadTSC() uint64 { return 1000 }

//go:nosplit
func ReadRFLAGS() uint64 { return 0x202 } // IF set

//go:nosplit
func RearmTimerNow() {}

//go:nosplit
func DisableTimerHardware() {}
