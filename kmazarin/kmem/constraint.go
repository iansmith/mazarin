// constraint.go — Constraint VM shared page allocation.
//
// Allocates a contiguous block of physical pages from the unified pool.
// These pages are mapped read-only into every shepherd's address space at
// a fixed VA, and writable by the kernel via PA + KernelVAOffset.

package kmem

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

// Constraint page layout constants.
const (
	ConstraintPageCount = 512                        // 512 × 4KB = 2MB
	ConstraintTotalSize = ConstraintPageCount * 4096 // 2MB
)

// Page header magic for constraint shared pages.
const ConstraintPageMagic = 0x4D415A46 // "MAZF"
const ConstraintPageVersion = 2        // bumped from 1: now includes region table

// Region layout offsets within the 2MB shared pages.
// Must match SharedPageHeader field values written during init.
const (
	RegionHeaderSize = 256 // SharedPageHeader is 256 bytes

	RegionNodeOff     = 0x0100   // 256 — attribute nodes
	RegionNodeSize    = 0x40000  // 256KB = 2048 × 128B
	RegionNodeCap     = 2048     // max attribute slots

	RegionEdgeOff     = 0x40100  // node end
	RegionEdgeSize    = 0x20000  // 128KB = 65536 × uint16
	RegionEdgeCap     = 65535    // max edges (uint16 cap)

	RegionBytecodeOff  = 0x60100  // edge end
	RegionBytecodeSize = 0x80000  // 512KB
	RegionBytecodeCap  = 32768    // max 16B instructions

	RegionStringOff   = 0xE0100  // bytecode end
	RegionStringSize  = 0x80000  // 512KB = 2048 × 256B
	RegionStringCap   = 2048     // max string slots

	RegionCollOff     = 0x160100 // string end
	RegionCollSize    = 0x10000  // 64KB = 2048 × 32B
	RegionCollCap     = 2048     // max collection elements

	RegionTrieOff     = 0x170100 // collection end
	RegionTrieSize    = 0x80000  // 512KB = 4096 × 128B
	RegionTrieCap     = 4096     // max trie nodes
)

// SharedPageHeader — first 256 bytes of the shared constraint pages.
// Readable by shepherds (read-only mapping). Written once during init.
type SharedPageHeader struct {
	Magic      uint32 // 0x4D415A46 "MAZF"
	Version    uint32 // 2
	Generation uint64 // bumped on attr destroy / trie mutation

	// Region table: offset + capacity pairs
	NodeRegionOff    uint32
	NodeCapacity     uint16
	_pad0            uint16
	EdgeRegionOff    uint32
	EdgeCapacity     uint16
	_pad1            uint16
	BytecodeRegionOff uint32
	BytecodeCapacity uint16
	_pad2            uint16
	StringRegionOff  uint32
	StringCapacity   uint16
	_pad3            uint16
	CollRegionOff    uint32
	CollCapacity     uint16
	_pad4            uint16
	TrieRegionOff    uint32
	TrieCapacity     uint16
	_pad5            uint16

	_reserved [256 - 64]byte
}

// Compile-time size assertion.
const _sharedPageHeaderSize = unsafe.Sizeof(SharedPageHeader{})
var _ [256 - _sharedPageHeaderSize]byte
var _ [_sharedPageHeaderSize - 256]byte

// constraintState holds the global constraint page allocation.
var constraintState struct {
	pa          uintptr // PA of first constraint page (contiguous)
	initialized uint32  // atomic: 0 = not yet, 1 = done
}

// InitConstraintPages allocates constraint shared pages from the unified pool.
// Safe to call multiple times; only the first call allocates.
//
//go:nosplit
func InitConstraintPages() bool {
	if atomic.LoadUint32(&constraintState.initialized) != 0 {
		return true
	}

	// Order 9 = 512 pages = 2MB (must match ConstraintTotalSize)
	pa := BuddyAllocTyped(9, PageConstraintShared, 0)
	if pa == 0 {
		serial.RawUARTPuts("[kmem] InitConstraintPages: allocation failed\r\n")
		return false
	}

	// Zero the entire region via kernel VA.
	va := pa + constants.KernelMMIOOffset
	ptr := (*[ConstraintTotalSize]byte)(unsafe.Pointer(va))
	for i := range ptr {
		ptr[i] = 0
	}

	// Write the full SharedPageHeader with region table.
	hdr := (*SharedPageHeader)(unsafe.Pointer(va))
	hdr.Magic = ConstraintPageMagic
	hdr.Version = ConstraintPageVersion
	hdr.Generation = 0

	hdr.NodeRegionOff = RegionNodeOff
	hdr.NodeCapacity = RegionNodeCap
	hdr.EdgeRegionOff = RegionEdgeOff
	hdr.EdgeCapacity = RegionEdgeCap
	hdr.BytecodeRegionOff = RegionBytecodeOff
	hdr.BytecodeCapacity = RegionBytecodeCap
	hdr.StringRegionOff = RegionStringOff
	hdr.StringCapacity = RegionStringCap
	hdr.CollRegionOff = RegionCollOff
	hdr.CollCapacity = RegionCollCap
	hdr.TrieRegionOff = RegionTrieOff
	hdr.TrieCapacity = RegionTrieCap

	constraintState.pa = pa
	atomic.StoreUint32(&constraintState.initialized, 1)

	serial.RawUARTPuts("[kmem] Constraint pages v2 at PA=0x")
	serial.RawUARTHex64(uint64(pa))
	serial.RawUARTPuts(" (")
	serial.RawUARTHex64(ConstraintTotalSize)
	serial.RawUARTPuts(" bytes)\r\n")

	return true
}

// GetConstraintPagePA returns the PA of the constraint shared pages.
// Returns 0 if not yet initialized.
//
//go:nosplit
func GetConstraintPagePA() uintptr {
	return constraintState.pa
}

// GetConstraintPageKernelVA returns the kernel VA for writing to constraint pages.
// Returns 0 if not yet initialized.
//
//go:nosplit
func GetConstraintPageKernelVA() uintptr {
	pa := constraintState.pa
	if pa == 0 {
		return 0
	}
	return pa + constants.KernelMMIOOffset
}

// MapUserConstraintPages maps the constraint shared pages read-only into the
// current shepherd's address space at the fixed VA.
// Must be called after TTBR0 has been switched to the shepherd's page table.
//
//go:nosplit
func MapUserConstraintPages() bool {
	// Lazy init: allocate on first shepherd launch.
	if !InitConstraintPages() {
		return false
	}

	pa := constraintState.pa
	const constraintVA = 0x00007FFD00000000 // must match UserConstraintPagesVA

	for i := uintptr(0); i < ConstraintPageCount; i++ {
		pageVA := uintptr(constraintVA) + i*PageSize
		pagePA := pa + i*PageSize
		// ELF_PF_R = read-only, normal cacheable memory.
		if !mapUserPage(pageVA, pagePA, ELF_PF_R) {
			serial.RawUARTPuts("[kmem] MapUserConstraintPages: failed at page ")
			serial.RawUARTHex64(uint64(i))
			serial.RawUARTPuts("\r\n")
			return false
		}
	}

	return true
}
