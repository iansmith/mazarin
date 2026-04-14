//go:build riscv64

package main

// ReadThreadRAX is a stub for RISC-V. See ipc_bridge_arm64.go for why this
// diagnostic is x86_64-specific.
//
//go:noinline
func ReadThreadRAX(pid int16, tid int32) uint64 {
	return 0
}
