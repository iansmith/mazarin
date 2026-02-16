package input

// platformInitInterrupts is a no-op on x86_64.
// VirtIO input uses polling via pollInputTopHalf() in the event poller.
func platformInitInterrupts() {}

// platformConfigureDeviceIRQ is a no-op on x86_64.
// Returns 0 (no IRQ). Input events are polled from the timer handler.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	return 0
}
