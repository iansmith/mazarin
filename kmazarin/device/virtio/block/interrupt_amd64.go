package block

// configureBlockInterrupt is a stub on x86_64.
// TODO: implement APIC-based INTx or MSI-X for x86_64 block devices.
func configureBlockInterrupt(_, _, _ uint8) uint32 {
	return 0
}
