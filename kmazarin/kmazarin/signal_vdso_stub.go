//go:build !riscv64 || test_stubs

package main

// initSigreturnVDSO is a no-op on ARM64 and x86_64.
// ARM64: EL0 can execute kernel pages via TTBR1 (no UXN bit set).
// x86_64: Uses a similar mechanism to ARM64 for the sigreturn trampoline.
func initSigreturnVDSO() {}
