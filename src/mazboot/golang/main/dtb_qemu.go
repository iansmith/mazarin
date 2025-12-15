//go:build qemuvirt && aarch64

package main

import "unsafe"

// Minimal FDT (Flattened Device Tree) parser for QEMU virt.
// We only need one thing: the PCI ECAM base+size so we can map it and avoid MMIO aborts.
//
// QEMU virt places the DTB in RAM; in this project we assume it is at 0x40000000.
// (We already map 0x40000000-0x40100000 as Device RO in initMMU.)

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

//go:nosplit
func cstring(p *byte) string {
	// Small, bounded scan (DTB strings are short). We stop at NUL.
	// NOTE: this allocates; keep use minimal and only during init.
	n := 0
	for {
		if *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n))) == 0 {
			break
		}
		n++
		// hard cap to avoid runaway in case of corrupted dtb
		if n > 256 {
			break
		}
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
}

//go:nosplit
func containsCompat(data *byte, n uint32, needle string) bool {
	// compatible is a NUL-separated string list.
	// We do a simple byte scan for the ASCII needle.
	nd := []byte(needle)
	if len(nd) == 0 || n < uint32(len(nd)) {
		return false
	}
	for i := uint32(0); i+uint32(len(nd)) <= n; i++ {
		match := true
		for j := 0; j < len(nd); j++ {
			if *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(data)) + uintptr(i) + uintptr(j))) != nd[j] {
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

var dtbPtr uintptr

//go:nosplit
func setDTBPtr(p uintptr) {
	dtbPtr = p
}

// getPciEcamFromDTB returns the ECAM base and size as described by the DTB.
// It looks for a node whose "compatible" contains "pci-host-ecam-generic" and reads its "reg".
//
// Assumptions (true for QEMU virt DTB):
// - #address-cells = 2, #size-cells = 2 for the PCI host node
// - reg[0] is the ECAM range: <addr_hi addr_lo size_hi size_lo>
//
//go:nosplit
func getPciEcamFromDTB() (base uintptr, size uintptr, ok bool) {
	dtbBase := dtbPtr
	if dtbBase == 0 {
		// Fallback only; in practice boot.s passes the DTB pointer via KernelMain(atags).
		dtbBase = uintptr(0x40000000)
	}
	hdr := (*byte)(unsafe.Pointer(dtbBase))
	if be32(hdr) != fdtMagic {
		return 0, 0, false
	}

	offStruct := be32((*byte)(unsafe.Pointer(dtbBase + 8)))
	offStrings := be32((*byte)(unsafe.Pointer(dtbBase + 12)))

	pStruct := dtbBase + uintptr(offStruct)
	pStrings := dtbBase + uintptr(offStrings)

	// stack of "is pci host node"
	var isPci [32]bool
	depth := -1

	p := pStruct
	for iter := 0; iter < 200000; iter++ { // hard cap
		tag := be32((*byte)(unsafe.Pointer(p)))
		p += 4
		switch tag {
		case fdtBeginNode:
			depth++
			if depth >= len(isPci) {
				return 0, 0, false
			}
			isPci[depth] = false
			// skip node name (NUL terminated), then align to 4
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
			name := cstring((*byte)(unsafe.Pointer(pStrings + uintptr(nameOff))))
			val := (*byte)(unsafe.Pointer(p))

			if depth >= 0 {
				if name == "compatible" && containsCompat(val, plen, "pci-host-ecam-generic") {
					isPci[depth] = true
				}
				if name == "reg" && isPci[depth] && plen >= 16 {
					addr := be64(val)
					sz := be64((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(val)) + 8)))
					if sz == 0 {
						return 0, 0, false
					}
					return uintptr(addr), uintptr(sz), true
				}
			}

			p += uintptr(plen)
			for (p & 3) != 0 {
				p++
			}
		case fdtNop:
			// nothing
		case fdtEnd:
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}


