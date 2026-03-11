//go:build !riscv64

package main

// setupExternalInterrupts is a no-op on ARM64 and AMD64.
// ARM64 uses GIC which is handled directly in assembly (exceptions_arm64.s).
// AMD64 uses APIC/IOAPIC handled through the IDT.
func setupExternalInterrupts() {}
