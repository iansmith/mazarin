//go:build !qemuvirt && !raspi

package main

// This stub exists to keep tooling that doesn't apply our build tags happy.
// Real implementations live in:
// - uart_qemu.go (qemuvirt && aarch64)
// - uart_rpi.go (raspi)
//
//go:nosplit
func uartPutc(c byte) {
	_ = c
}


