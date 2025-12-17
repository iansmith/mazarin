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

// dumpBytesHex prints up to max bytes from p as hex, without allocations.
func dumpBytesHex(p *byte, n uint32, max uint32) {
	if n > max {
		n = max
	}
	for i := uint32(0); i < n; i++ {
		b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i)))
		uartPutHex8(uint8(b))
	}
	uartPuts("\r\n")
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
	uartPutc('t') // Breadcrumb: tryDTBAtBase entry

	hdr := (*byte)(unsafe.Pointer(dtbBase))
	magic := be32(hdr)
	if magic != fdtMagic {
		uartPutc('m') // Breadcrumb: magic mismatch
		return 0, 0, false
	}

	offStruct := be32((*byte)(unsafe.Pointer(dtbBase + 8)))
	offStrings := be32((*byte)(unsafe.Pointer(dtbBase + 12)))

	pStruct := dtbBase + uintptr(offStruct)
	pStrings := dtbBase + uintptr(offStrings)

	// Per-depth state for the current node:
	// - compatMatch: node's "compatible" contains "pci-host-ecam-generic"
	// - haveReg:     node has a "reg" property we can interpret as ECAM window
	// - regAddr/regSize: decoded from that "reg" (addr_hi/lo, size_hi/lo)
	var compatMatch [32]bool
	var haveReg [32]bool
	var regAddr [32]uintptr
	var regSize [32]uintptr
	depth := -1

	p := pStruct
	for iter := 0; iter < 200000; iter++ { // hard cap
		tag := be32((*byte)(unsafe.Pointer(p)))
		p += 4
		switch tag {
		case fdtBeginNode:
			uartPutc('n') // Breadcrumb: begin node
			depth++
			if depth >= len(compatMatch) {
				return 0, 0, false
			}
			compatMatch[depth] = false
			haveReg[depth] = false
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
			namePtr := (*byte)(unsafe.Pointer(pStrings + uintptr(nameOff)))
			val := (*byte)(unsafe.Pointer(p))

			if depth >= 0 {
				// Order-independent handling: we might see "reg" before "compatible"
				// or vice versa. Record both and as soon as we have both for a node,
				// return the ECAM window.

				// Handle "compatible" first.
				// Handle "compatible" first.
				if dtbNameIsCompatible(namePtr) {
					uartPuts("DTB: compatible at depth ")
					uartPutUint32(uint32(depth))
					uartPuts(" plen=0x")
					uartPutHex32(plen)
					uartPuts(" value[0:32]=")
					dumpBytesHex(val, plen, 32)

					if dtbCompatHasPciHostEcamGeneric(val, plen) {
						uartPutc('c') // Breadcrumb: found compatible with pci-host-ecam-generic
						compatMatch[depth] = true
						if haveReg[depth] {
							uartPutc('R') // Breadcrumb: successful ECAM extraction (compat last)
							return regAddr[depth], regSize[depth], true
						}
					}
				}

				// Handle "reg" (may appear before or after "compatible").
				if dtbNameIsReg(namePtr) && plen >= 16 {
					uartPuts("DTB: reg at depth ")
					uartPutUint32(uint32(depth))
					uartPuts(" plen=0x")
					uartPutHex32(plen)
					uartPuts(" bytes=")
					dumpBytesHex(val, 16, 16)

					uartPutc('r') // Breadcrumb: saw reg property
					addr := be64(val)
					sz := be64((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(val)) + 8)))
					if sz == 0 {
						uartPutc('z') // Breadcrumb: zero size, treat as failure
						return 0, 0, false
					}
					regAddr[depth] = uintptr(addr)
					regSize[depth] = uintptr(sz)
					haveReg[depth] = true
					if compatMatch[depth] {
						uartPutc('R') // Breadcrumb: successful ECAM extraction (reg last)
						return regAddr[depth], regSize[depth], true
					}
				}
			}

			p += uintptr(plen)
			for (p & 3) != 0 {
				p++
			}
		case fdtNop:
			// nothing
		case fdtEnd:
			uartPutc('e') // Breadcrumb: reached FDT_END without finding ECAM
			return 0, 0, false
		default:
			uartPutc('E') // Breadcrumb: unexpected tag
			return 0, 0, false
		}
	}
	uartPutc('F') // Breadcrumb: iteration cap reached
	return 0, 0, false
}

// getPciEcamFromDTB returns the ECAM base and size as described by the DTB.
// It looks for a node whose "compatible" contains "pci-host-ecam-generic"
// and reads its "reg" property.
//
// Assumptions (true for QEMU virt DTB):
// - #address-cells = 2, #size-cells = 2 for the PCI host node
// - reg[0] is the ECAM range: <addr_hi addr_lo size_hi size_lo>
func getPciEcamFromDTB() (base uintptr, size uintptr, ok bool) {
	uartPutc('G') // Breadcrumb: getPciEcamFromDTB entry

	// Try candidates in order:
	//  1) DTB pointer passed from boot.s via KernelMain(atags) (Linux protocol)
	//  2) Physical 0x0 (some QEMU configurations place DTB at bottom of address space)
	//  3) Physical 0x40000000 (start of RAM for virt in our layout)

	// 1) Pointer from boot (if any)
	if dtbPtr != 0 {
		uartPutc('1') // Breadcrumb: trying dtbPtr
		if base, size, ok := tryDTBAtBase(dtbPtr); ok {
			uartPutc('!') // Breadcrumb: success via dtbPtr
			return base, size, true
		}
	}

	// 2) Physical 0x0
	uartPutc('2') // Breadcrumb: trying DTB at 0x0
	if base, size, ok := tryDTBAtBase(0); ok {
		uartPutc('@') // Breadcrumb: success via 0x0
		return base, size, true
	}

	// 3) Physical 0x40000000
	uartPutc('3') // Breadcrumb: trying DTB at 0x40000000
	if base, size, ok := tryDTBAtBase(uintptr(0x40000000)); ok {
		uartPutc('#') // Breadcrumb: success via 0x40000000
		return base, size, true
	}

	uartPutc('g') // Breadcrumb: getPciEcamFromDTB failure
	return 0, 0, false
}

// initDeviceTree parses the DTB (if present) after the MMU is enabled and
// memory attributes are correctly set, and applies any configuration we
// care about (currently: PCI ECAM base).
func initDeviceTree() {
	uartPuts("DTB: initDeviceTree() entry\r\n")
	uartPutc('I') // Breadcrumb: initDeviceTree start

	base, size, ok := getPciEcamFromDTB()
	if !ok {
		uartPuts("DTB: getPciEcamFromDTB() failed (no pci-host-ecam-generic)\r\n")
		uartPutc('i') // Breadcrumb: initDeviceTree early exit
		return
	}

	// Show what we discovered from the DTB and what the ECAM base was before
	// overriding it, so we can compare DTB vs. fallback/initial value.
	uartPuts("DTB: PCI ECAM from DTB: base=0x")
	uartPutHex64(uint64(base))
	uartPuts(" size=0x")
	uartPutHex64(uint64(size))
	uartPuts("\r\n")

	uartPuts("DTB: pciEcamBase before override: 0x")
	uartPutHex64(uint64(pciEcamBase))
	uartPuts("\r\n")

	// Set runtime ECAM base so PCI code uses the DTB-provided value.
	// On QEMU virt with virtualization=on this should be 0x4010000000.
	setPciEcamBase(base)
}
