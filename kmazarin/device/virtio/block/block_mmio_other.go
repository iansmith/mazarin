package block

// findVirtIOBlockMMIO is a no-op on non-RISC-V architectures.
// VirtIO MMIO transport is only used on RISC-V QEMU virt.
func findVirtIOBlockMMIO() bool {
	return false
}

// virtioBlockInitMMIO is a no-op on non-RISC-V architectures.
func virtioBlockInitMMIO() bool {
	return false
}

// virtioBlockReadConfigMMIO is a no-op on non-RISC-V architectures.
func virtioBlockReadConfigMMIO() bool {
	return false
}
