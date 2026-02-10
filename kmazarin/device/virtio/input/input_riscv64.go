package input

// platformInitInterrupts is a no-op on RISC-V.
// VirtIO input uses polling via PollAllDevices() called from the timer handler.
func platformInitInterrupts() {}

// platformConfigureDeviceIRQ is a no-op on RISC-V.
// Returns 0 (no IRQ). Input events are polled from the timer handler.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	return 0
}
