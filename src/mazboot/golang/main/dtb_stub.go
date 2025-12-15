//go:build !qemuvirt || !aarch64

package main

//go:nosplit
func getPciEcamFromDTB() (base uintptr, size uintptr, ok bool) {
	return 0, 0, false
}

//go:nosplit
func setDTBPtr(p uintptr) {
	_ = p
}


