//go:build riscv64 && !test_stubs

package main

// RISC-V-specific assembly function declarations.
// Implementations are in gic_riscv64.s and runtime_riscv64.s.

// ReadTime reads the time CSR (mtime equivalent accessible from S-mode).
// Returns the current cycle counter value.
//
//go:nosplit
func ReadTime() uint64

// ReadSSTATUS reads the sstatus CSR.
// Used to check interrupt enable state (SIE bit, bit 1).
//
//go:nosplit
func ReadSSTATUS() uint64

// RearmTimerNow re-arms the S-mode timer via SBI set_timer.
//
//go:nosplit
func RearmTimerNow()

// DisableTimerHardware disables the S-mode timer interrupt in sie CSR.
//
//go:nosplit
func DisableTimerHardware()

// DumpInstructionPageFaultAsm is the ABI0 entry point for instruction page fault diagnostics.
// Called from exception handler when an instruction page fault occurs on a pre-mapped code page.
// Walks the Sv48 page table and prints each level's PTE to identify the corruption.
//
//go:nosplit
func DumpInstructionPageFaultAsm(faultAddr uint64)
