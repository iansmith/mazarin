//go:build (!amd64 || test_stubs)

package main

// initCOM1Uart is a no-op on ARM64 and RISC-V.
// These platforms discover UART from the device tree.
func initCOM1Uart() {}
