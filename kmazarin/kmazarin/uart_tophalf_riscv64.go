//go:build riscv64 && !test_stubs

package main

// uartTopHalf is a no-op on RISC-V. The PLIC dispatch handler
// (plicDispatchIRQInternal) calls ns16550UartTopHalf directly
// before NonTimerIRQTopHalf is ever reached for UART IRQs.
//
//go:nosplit
func uartTopHalf(_ uint32) {}
