//go:build riscv64

// diplomat/main/dtb_hwcap_riscv64.go - AT_HWCAP for RISC-V
//
// Computes the Linux HWCAP bitmask for RISC-V CPUs.
// On RISC-V, ISA extensions are typically read from the Device Tree
// (riscv,isa property in CPU nodes) rather than CPU registers.

package main

// Linux HWCAP bit definitions for RISC-V
// These match the Linux kernel's <asm/hwcap.h> for RISC-V
const (
	hwcapISA_I = 1 << ('I' - 'A') // Integer
	hwcapISA_M = 1 << ('M' - 'A') // Integer multiply/divide
	hwcapISA_A = 1 << ('A' - 'A') // Atomic instructions
	hwcapISA_F = 1 << ('F' - 'A') // Single-precision FP
	hwcapISA_D = 1 << ('D' - 'A') // Double-precision FP
	hwcapISA_C = 1 << ('C' - 'A') // Compressed instructions
)

// computeHWCAP returns the Linux HWCAP bitmask for RISC-V.
// For QEMU virt platform, we assume RV64IMAFDC (G extension minus N).
//
// In a full implementation, this would parse the DTB's /cpus/cpu@0/riscv,isa
// property, but for now we return a reasonable default for QEMU.
func computeHWCAP() uint64 {
	// QEMU virt platform typically provides RV64IMAFDC
	// This is the "G" extension (general-purpose) minus "N" (user-mode interrupts)
	var hwcap uint64 = hwcapISA_I | hwcapISA_M | hwcapISA_A | hwcapISA_F | hwcapISA_D | hwcapISA_C

	// TODO: Parse DTB to read actual ISA string from /cpus/cpu@0/riscv,isa
	// and compute hwcap from that. For now, hardcode the common case.

	return hwcap
}
