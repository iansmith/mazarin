package block

// configureBlockInterrupt is a no-op on RISC-V.
// RISC-V uses MMIO transport with PLIC, not PCI INTx.
func configureBlockInterrupt(_, _, _ uint8) uint32 {
	return 0
}
