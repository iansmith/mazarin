//go:build qemuvirt && aarch64

package main

//go:nosplit
func setPciEcamBase(base uintptr) {
	pciEcamBase = base
}
