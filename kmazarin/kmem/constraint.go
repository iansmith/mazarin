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

// Page header magic for constraint shared pages.
const ConstraintPageMagic = 0x4D415A46 // "MAZF"
const ConstraintPageVersion = 2        // bumped from 1: now includes region table

// --- Single source of truth: region capacities ---
// Change ONLY these values to resize the constraint system.
const (
	RegionNodeCap     = 16384 // max attribute node slots
	RegionEdgeCap     = 65535 // max edges (uint16 cap)
	RegionBytecodeCap = 65535 // header field (uint16); actual limit is RegionBytecodeSize
	RegionStringCap   = 4096  // max string slots
	RegionCollCap     = 4096  // max collection elements
	RegionTrieCap     = 16384 // max trie nodes
)

// --- Per-slot sizes (from flat package, duplicated here to avoid import) ---
const (
	attrNodeSize   = 128 // flat.AttrNodeSize
	stringSlotSize = 256 // flat.StringSlotSize
	valueSize      = 40  // flat.ValueSize
)

// --- Derived region sizes and offsets (do NOT edit manually) ---
const (
	RegionHeaderSize = 256

	RegionNodeOff  = RegionHeaderSize
	RegionNodeSize = RegionNodeCap * attrNodeSize

	RegionEdgeOff  = RegionNodeOff + RegionNodeSize
	RegionEdgeSize = RegionEdgeCap * 2 // uint16 per edge

	RegionBytecodeOff  = RegionEdgeOff + RegionEdgeSize
	RegionBytecodeSize = 1 << 20 // 1MB

	RegionStringOff  = RegionBytecodeOff + RegionBytecodeSize
	RegionStringSize = RegionStringCap * stringSlotSize

	RegionCollOff  = RegionStringOff + RegionStringSize
	RegionCollSize = RegionCollCap * valueSize

	RegionTrieOff  = RegionCollOff + RegionCollSize
	RegionTrieSize = RegionTrieCap * 128 // trie node size

	// Total footprint: header through trie end, rounded up to page size.
	constraintEndOff    = RegionTrieOff + RegionTrieSize
	ConstraintTotalSize = (constraintEndOff + 4095) &^ 4095
	ConstraintPageCount = ConstraintTotalSize / 4096
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

	// Derive buddy order from ConstraintTotalSize.
	// Order N = 2^N pages = 2^N × 4KB.
	order := 0
	for (1 << order) * 4096 < ConstraintTotalSize {
		order++
	}
	pa := BuddyAllocTyped(order, PageConstraintShared, 0)
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
	return MapUserConstraintPagesWithL0(0)
}

// MapUserConstraintPagesWithL0 maps the constraint shared pages read-only
// using an explicit L0 page table PA. If l0PA is 0, reads from TTBR0.
//
//go:nosplit
func MapUserConstraintPagesWithL0(l0PA uintptr) bool {
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
		if !mapUserPageWithL0(pageVA, pagePA, ELF_PF_R, l0PA) {
			serial.RawUARTPuts("[kmem] MapUserConstraintPages: failed at page ")
			serial.RawUARTHex64(uint64(i))
			serial.RawUARTPuts("\r\n")
			return false
		}
	}

	return true
}
