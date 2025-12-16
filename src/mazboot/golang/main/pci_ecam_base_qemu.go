//go:build qemuvirt && aarch64

package main

//go:nosplit
func setPciEcamBase(base uintptr) {
	// Minimal debug - just set the value
	pciEcamBase = base
	// Print after assignment to verify it worked
	uartPuts("PCI: ECAM base set to 0x")
	uartPutHex64(uint64(pciEcamBase))
	uartPuts("\r\n")
}
