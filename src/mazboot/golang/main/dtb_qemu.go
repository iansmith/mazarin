//go:build qemuvirt && aarch64

package main

import "unsafe"

// Minimal FDT (Flattened Device Tree) parser for QEMU virt.
// We only need one thing: the PCI ECAM base+size so we can map it and avoid MMIO aborts.
//
// QEMU virt places the DTB in RAM; in this project we *assume* it is available
// either via a pointer passed in x0 (Linux kernel boot protocol) or at a fixed
// physical address (commonly 0x40000000 = start of RAM for virt). Our code
// tries the pointer first and then falls back to the fixed address.

const (
	fdtMagic = 0xd00dfeed

	fdtBeginNode = 1
	fdtEndNode   = 2
	fdtProp      = 3
	fdtNop       = 4
	fdtEnd       = 9
)

//go:nosplit
func be32(p *byte) uint32 {
	b := *(*[4]byte)(unsafe.Pointer(p))
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

//go:nosplit
func be64(p *byte) uint64 {
	b := *(*[8]byte)(unsafe.Pointer(p))
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}

// DTB string constants as raw bytes (no Go string/[]byte conversions).
var (
	dtbNameCompatible    = [...]byte{'c', 'o', 'm', 'p', 'a', 't', 'i', 'b', 'l', 'e'}
	dtbNameReg           = [...]byte{'r', 'e', 'g'}
	dtbCompatPciHostEcam = [...]byte{'p', 'c', 'i', '-', 'h', 'o', 's', 't', '-', 'e', 'c', 'a', 'm', '-', 'g', 'e', 'n', 'e', 'r', 'i', 'c'}
)

// dumpBytesHex prints up to max bytes from p as hex (for debugging only)
func dumpBytesHex(p *byte, n uint32, max uint32) {
	// Disabled for clean output
}

// dtbNameEqualsLiteral compares the NUL-terminated C string at p with a fixed
// literal (needle) of length n. It returns true only if the first n bytes
// match and the (n+1)th byte is NUL (exact match, no prefix).
func dtbNameEqualsLiteral(p *byte, needle *byte, n int) bool {
	for i := 0; i < n; i++ {
		b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i)))
		nb := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(needle)) + uintptr(i)))
		if b != nb {
			return false
		}
	}
	// Require trailing NUL in DTB name.
	terminator := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n)))
	return terminator == 0
}

func dtbNameIsCompatible(p *byte) bool {
	return dtbNameEqualsLiteral(p, &dtbNameCompatible[0], len(dtbNameCompatible))
}

func dtbNameIsReg(p *byte) bool {
	return dtbNameEqualsLiteral(p, &dtbNameReg[0], len(dtbNameReg))
}

// dtbCompatContains scans a DTB "compatible" value (NUL-separated strings)
// for the given ASCII needle (provided as raw bytes).
func dtbCompatContains(data *byte, n uint32, needle *byte, needleLen int) bool {
	if n == 0 || n < uint32(needleLen) {
		return false
	}
	for i := uint32(0); i+uint32(needleLen) <= n; i++ {
		match := true
		for j := 0; j < needleLen; j++ {
			b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(data)) + uintptr(i) + uintptr(j)))
			nb := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(needle)) + uintptr(j)))
			if b != nb {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func dtbCompatHasPciHostEcamGeneric(data *byte, n uint32) bool {
	return dtbCompatContains(data, n, &dtbCompatPciHostEcam[0], len(dtbCompatPciHostEcam))
}

// dtbPtr is set early from KernelMain(atags) when QEMU passes a DTB pointer
// in x0 using the Linux kernel boot protocol. When that isn't available we
// fall back to a fixed physical address.
var dtbPtr uintptr

//go:nosplit
func setDTBPtr(p uintptr) {
	dtbPtr = p
}

// tryDTBAtBase attempts to interpret a DTB located at dtbBase and, if valid,
// extract the PCI ECAM base/size from a "pci-host-ecam-generic" node.
func tryDTBAtBase(dtbBase uintptr) (base uintptr, size uintptr, ok bool) {
	hdr := (*byte)(unsafe.Pointer(dtbBase))
	magic := be32(hdr)
	if magic != fdtMagic {
		return 0, 0, false
	}

	offStruct := be32((*byte)(unsafe.Pointer(dtbBase + 8)))
	offStrings := be32((*byte)(unsafe.Pointer(dtbBase + 12)))
	pStruct := dtbBase + uintptr(offStruct)
	pStrings := dtbBase + uintptr(offStrings)

	var compatMatch [32]bool
	var haveReg [32]bool
	var regAddr [32]uintptr
	var regSize [32]uintptr
	depth := -1

	p := pStruct
	for iter := 0; iter < 200000; iter++ {
		tag := be32((*byte)(unsafe.Pointer(p)))
		p += 4
		switch tag {
		case fdtBeginNode:
			depth++
			if depth >= len(compatMatch) {
				return 0, 0, false
			}
			compatMatch[depth] = false
			haveReg[depth] = false
			for {
				b := *(*byte)(unsafe.Pointer(p))
				p++
				if b == 0 {
					break
				}
			}
			for (p & 3) != 0 {
				p++
			}
		case fdtEndNode:
			depth--
			if depth < -1 {
				return 0, 0, false
			}
		case fdtProp:
			plen := be32((*byte)(unsafe.Pointer(p)))
			nameOff := be32((*byte)(unsafe.Pointer(p + 4)))
			p += 8
			namePtr := (*byte)(unsafe.Pointer(pStrings + uintptr(nameOff)))
			val := (*byte)(unsafe.Pointer(p))

			if depth >= 0 {
				if dtbNameIsCompatible(namePtr) {
					if dtbCompatHasPciHostEcamGeneric(val, plen) {
						compatMatch[depth] = true
						if haveReg[depth] {
							return regAddr[depth], regSize[depth], true
						}
					}
				}

				if dtbNameIsReg(namePtr) && plen >= 16 {
					addr := be64(val)
					sz := be64((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(val)) + 8)))
					if sz == 0 {
						return 0, 0, false
					}
					regAddr[depth] = uintptr(addr)
					regSize[depth] = uintptr(sz)
					haveReg[depth] = true
					if compatMatch[depth] {
						return regAddr[depth], regSize[depth], true
					}
				}
			}

			p += uintptr(plen)
			for (p & 3) != 0 {
				p++
			}
		case fdtNop:
		case fdtEnd:
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// getPciEcamFromDTB returns the ECAM base and size as described by the DTB.
func getPciEcamFromDTB() (base uintptr, size uintptr, ok bool) {
	// Try: 1) DTB pointer from boot, 2) Physical 0x0, 3) Physical 0x40000000
	if dtbPtr != 0 {
		if base, size, ok := tryDTBAtBase(dtbPtr); ok {
			return base, size, true
		}
	}
	if base, size, ok := tryDTBAtBase(0); ok {
		return base, size, true
	}
	if base, size, ok := tryDTBAtBase(uintptr(0x40000000)); ok {
		return base, size, true
	}
	return 0, 0, false
}

// initDeviceTree parses the DTB and applies configuration (PCI ECAM base).
func initDeviceTree() {
	base, _, ok := getPciEcamFromDTB()
	if !ok {
		return
	}
	setPciEcamBase(base)
}
