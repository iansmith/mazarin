//go:build qemuvirt && aarch64

package main

import "unsafe"

// Simple LFSR for pseudo-random number generation
var rngState uint32 = 0xACE1  // Non-zero seed

//go:nosplit
func nextRandom() byte {
	// 32-bit LFSR with taps at positions 32, 22, 2, 1
	lsb := rngState & 1
	rngState >>= 1
	if lsb != 0 {
		rngState ^= 0x80200003
	}
	return byte(rngState)
}

// getRandomBytes returns pseudo-random bytes for Cardinal
// Cardinal no longer initializes VirtIO RNG - kmazarin handles that
// This stub returns simple pseudo-random data for syscalls
//
//go:nosplit
func getRandomBytes(buf unsafe.Pointer, length uint32) uint32 {
	ptr := (*[1 << 30]byte)(buf)
	for i := uint32(0); i < length; i++ {
		ptr[i] = nextRandom()
	}
	return length
}
