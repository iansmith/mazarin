//go:build riscv64

// diplomat/main/fdt_parse.go - Flattened Device Tree (FDT/DTB) parser
//
// Parses the FDT passed by OpenSBI in register A1 to extract:
//   - Memory layout (/memory node)
//   - Reserved regions (/reserved-memory)
//   - CPU count and ISA extensions (/cpus)
//   - Boot arguments (/chosen)
//
// Reference: Devicetree Specification v0.4
// https://devicetree-specification.readthedocs.io/

package main

import "unsafe"

// FDT Header (big-endian)
type fdtHeader struct {
	magic           uint32 // 0xD00DFEED
	totalsize       uint32 // Total size of DTB
	off_dt_struct   uint32 // Offset to structure block
	off_dt_strings  uint32 // Offset to strings block
	off_mem_rsvmap  uint32 // Offset to memory reservation block
	version         uint32 // Version (should be >= 17)
	last_comp_version uint32 // Last compatible version
	boot_cpuid_phys uint32 // Physical ID of boot CPU
	size_dt_strings uint32 // Size of strings block
	size_dt_struct  uint32 // Size of structure block
}

// FDT tokens (big-endian)
const (
	FDT_BEGIN_NODE = 0x00000001
	FDT_END_NODE   = 0x00000002
	FDT_PROP       = 0x00000003
	FDT_NOP        = 0x00000004
	FDT_END        = 0x00000009
)

// FDTInfo holds parsed information from the device tree
type FDTInfo struct {
	RAMBase      uint64 // Physical address of RAM start
	RAMSize      uint64 // Size of RAM in bytes
	ReservedBase uint64 // OpenSBI reserved region base (if any)
	ReservedSize uint64 // OpenSBI reserved region size
	CPUCount     uint32 // Number of CPU harts
	ISAString    string // ISA extensions string (e.g., "rv64imafdcsu")
	FDTAddress   uint64 // Physical address of FDT itself
}

// parseFDT parses the FDT and extracts key information
//
//go:nosplit
func parseFDT(fdtAddr uint64) (*FDTInfo, error) {
	if fdtAddr == 0 {
		printString("ERROR: FDT address is zero\r\n")
		return nil, &fdtError{"FDT address is zero"}
	}

	printString("Parsing FDT at PA 0x")
	printHex(fdtAddr)
	printString("\r\n")

	// Read FDT header
	hdr := (*fdtHeader)(unsafe.Pointer(uintptr(fdtAddr)))

	// Verify magic (big-endian 0xD00DFEED)
	magic := be32toh(hdr.magic)
	if magic != 0xD00DFEED {
		printString("ERROR: Invalid FDT magic: 0x")
		printHex(uint64(magic))
		printString(" (expected 0xD00DFEED)\r\n")
		return nil, &fdtError{"Invalid FDT magic"}
	}

	totalsize := be32toh(hdr.totalsize)
	version := be32toh(hdr.version)

	printString("FDT: magic OK, size=")
	printHex(uint64(totalsize))
	printString(" version=")
	printHex(uint64(version))
	printString("\r\n")

	// Initialize result
	info := &FDTInfo{
		FDTAddress: fdtAddr,
	}

	// Parse structure block to find nodes
	structBase := uintptr(fdtAddr) + uintptr(be32toh(hdr.off_dt_struct))
	stringsBase := uintptr(fdtAddr) + uintptr(be32toh(hdr.off_dt_strings))

	// Walk the device tree
	offset := structBase
	depth := 0
	currentNode := ""

	for {
		token := be32toh(*(*uint32)(unsafe.Pointer(offset)))
		offset += 4

		switch token {
		case FDT_BEGIN_NODE:
			// Node name follows (null-terminated string)
			name := readCString(offset)
			if depth == 0 {
				currentNode = name
			}

			// Align to 4-byte boundary after name
			nameLen := cstrlen(offset)
			offset += (nameLen + 4) &^ 3
			depth++

		case FDT_END_NODE:
			depth--
			if depth == 0 {
				currentNode = ""
			}

		case FDT_PROP:
			// Property: len(4) nameoff(4) data(len)
			propLen := be32toh(*(*uint32)(unsafe.Pointer(offset)))
			offset += 4
			nameOff := be32toh(*(*uint32)(unsafe.Pointer(offset)))
			offset += 4

			propName := readCString(stringsBase + uintptr(nameOff))
			propData := offset

			// Parse relevant properties
			if currentNode == "memory" || currentNode[:7] == "memory@" {
				if propName == "reg" && propLen >= 16 {
					// reg property contains pairs of (address, size) as 64-bit values
					info.RAMBase = be64toh(*(*uint64)(unsafe.Pointer(propData)))
					info.RAMSize = be64toh(*(*uint64)(unsafe.Pointer(propData + 8)))
				}
			} else if currentNode == "cpus" {
				// Count CPU nodes by looking for "cpu@" children (handled in BEGIN_NODE)
			} else if currentNode[:4] == "cpu@" {
				info.CPUCount++
				if propName == "riscv,isa" {
					// ISA string (e.g., "rv64imafdcsu")
					info.ISAString = readCStringLen(propData, int(propLen))
				}
			} else if currentNode == "reserved-memory" || currentNode[:16] == "reserved-memory@" {
				if propName == "reg" && propLen >= 16 {
					// First reserved region (typically OpenSBI)
					if info.ReservedBase == 0 {
						info.ReservedBase = be64toh(*(*uint64)(unsafe.Pointer(propData)))
						info.ReservedSize = be64toh(*(*uint64)(unsafe.Pointer(propData + 8)))
					}
				}
			}

			// Align to 4-byte boundary after data
			offset += (uintptr(propLen) + 3) &^ 3

		case FDT_NOP:
			// Skip

		case FDT_END:
			// End of structure block
			goto done

		default:
			printString("ERROR: Unknown FDT token: 0x")
			printHex(uint64(token))
			printString("\r\n")
			return nil, &fdtError{"Unknown FDT token"}
		}
	}

done:
	printString("FDT parsed: RAM 0x")
	printHex(info.RAMBase)
	printString("-0x")
	printHex(info.RAMBase + info.RAMSize)
	printString(" (")
	printHex(info.RAMSize / (1024 * 1024))
	printString("MB), CPUs=")
	printHex(uint64(info.CPUCount))
	printString("\r\n")

	if info.ISAString != "" {
		printString("ISA: ")
		printString(info.ISAString)
		printString("\r\n")
	}

	if info.ReservedBase != 0 {
		printString("Reserved: 0x")
		printHex(info.ReservedBase)
		printString("-0x")
		printHex(info.ReservedBase + info.ReservedSize)
		printString("\r\n")
	}

	return info, nil
}

// Big-endian conversion helpers

//go:nosplit
func be32toh(v uint32) uint32 {
	return (v>>24)&0xFF | (v>>8)&0xFF00 | (v<<8)&0xFF0000 | (v<<24)&0xFF000000
}

//go:nosplit
func be64toh(v uint64) uint64 {
	return uint64(be32toh(uint32(v>>32)))<<32 | uint64(be32toh(uint32(v)))
}

// String helpers

//go:nosplit
func readCString(addr uintptr) string {
	// Find null terminator
	end := addr
	for *(*byte)(unsafe.Pointer(end)) != 0 {
		end++
		if end-addr > 256 { // Safety limit
			break
		}
	}

	// Convert to Go string (copy to avoid keeping FDT memory referenced)
	length := end - addr
	if length == 0 {
		return ""
	}

	// Build string manually (can't use make/slice in nosplit)
	bytes := (*[256]byte)(unsafe.Pointer(addr))
	result := ""
	for i := uintptr(0); i < length && i < 256; i++ {
		result += string(bytes[i])
	}
	return result
}

//go:nosplit
func readCStringLen(addr uintptr, maxLen int) string {
	if maxLen > 256 {
		maxLen = 256
	}

	bytes := (*[256]byte)(unsafe.Pointer(addr))
	result := ""
	for i := 0; i < maxLen; i++ {
		if bytes[i] == 0 {
			break
		}
		result += string(bytes[i])
	}
	return result
}

//go:nosplit
func cstrlen(addr uintptr) uintptr {
	end := addr
	for *(*byte)(unsafe.Pointer(end)) != 0 {
		end++
		if end-addr > 4096 {
			break
		}
	}
	return end - addr + 1 // Include null terminator
}

// Error type for FDT parsing
type fdtError struct {
	msg string
}

func (e *fdtError) Error() string {
	return "FDT: " + e.msg
}

var (
	errFDTInvalidMagic = fdtError{"Invalid FDT magic"}
	errFDTZeroAddress  = fdtError{"FDT address is zero"}
)
