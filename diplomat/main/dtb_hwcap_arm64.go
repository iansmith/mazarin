//go:build arm64

// diplomat/main/dtb_hwcap_arm64.go - Dynamic AT_HWCAP from CPU ID registers
//
// Reads ID_AA64ISAR0_EL1 and ID_AA64PFR0_EL1 to compute the Linux HWCAP
// bitmask. This replaces the hardcoded 0x2 (ASIMD-only) value.

package main

// Linux HWCAP bit definitions for ARM64
const (
	hwcapFP      = 1 << 0
	hwcapASIMD   = 1 << 1
	hwcapAES     = 1 << 3
	hwcapPMULL   = 1 << 4
	hwcapSHA1    = 1 << 5
	hwcapSHA2    = 1 << 6
	hwcapCRC32   = 1 << 7
	hwcapATOMICS = 1 << 8
)

// computeHWCAP reads ARM64 CPU ID registers and returns the Linux HWCAP bitmask.
func computeHWCAP() uint64 {
	isar0 := readIdAA64ISAR0()
	pfr0 := readIdAA64PFR0()

	var hwcap uint64

	// FP: PFR0[19:16] == 0 means FP supported (0xF means not supported)
	fp := (pfr0 >> 16) & 0xF
	if fp == 0 {
		hwcap |= hwcapFP
	}

	// ASIMD: PFR0[23:20] == 0 means ASIMD supported (0xF means not supported)
	asimd := (pfr0 >> 20) & 0xF
	if asimd == 0 {
		hwcap |= hwcapASIMD
	}

	// AES: ISAR0[7:4] >= 1
	aes := (isar0 >> 4) & 0xF
	if aes >= 1 {
		hwcap |= hwcapAES
	}
	// PMULL: ISAR0[7:4] >= 2
	if aes >= 2 {
		hwcap |= hwcapPMULL
	}

	// SHA1: ISAR0[11:8] >= 1
	sha1 := (isar0 >> 8) & 0xF
	if sha1 >= 1 {
		hwcap |= hwcapSHA1
	}

	// SHA2: ISAR0[15:12] >= 1
	sha2 := (isar0 >> 12) & 0xF
	if sha2 >= 1 {
		hwcap |= hwcapSHA2
	}

	// CRC32: ISAR0[19:16] >= 1
	crc32 := (isar0 >> 16) & 0xF
	if crc32 >= 1 {
		hwcap |= hwcapCRC32
	}

	// ATOMICS (LSE): ISAR0[23:20] >= 2
	atomics := (isar0 >> 20) & 0xF
	if atomics >= 2 {
		hwcap |= hwcapATOMICS
	}

	return hwcap
}

// Assembly declarations — defined in pagetable_arm64.s
func readIdAA64ISAR0() uint64
func readIdAA64PFR0() uint64
