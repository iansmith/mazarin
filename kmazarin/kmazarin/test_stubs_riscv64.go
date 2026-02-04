//go:build test_stubs && riscv64

package main

// RISC-V-specific stub implementations for test builds.
// These match the declarations in asm_decl_riscv64.go.

//go:nosplit
func ReadTime() uint64 { return 1000 }

//go:nosplit
func ReadSSTATUS() uint64 { return 0 }

//go:nosplit
func RearmTimerNow() {}

//go:nosplit
func DisableTimerHardware() {}
