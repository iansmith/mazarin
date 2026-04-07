// constraint_mgr.go — Kernel-side attribute manager for shared constraint pages.
//
// KernelAttrManager wraps the raw shared page VA and provides allocators for
// nodes, strings, edges, trie nodes, bytecode, and collections. This struct
// lives in kernel BSS — only the data written into the shared pages is visible
// to shepherds (read-only).

package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/mazarin/vm/flat"
	"unsafe"
)

// MaxQueryPatterns is the maximum number of registered find patterns.
const MaxQueryPatterns = 64

// maxURILen is the maximum URI length accepted by syscalls.
const maxURILen = 1024

// maxURISegments is the maximum number of URI segments.
const maxURISegments = 16

// queryPattern holds a registered find pattern and its result attribute.
type queryPattern struct {
	pattern    [256]byte
	patternLen uint16
	resultSlot uint16
	ownerSID   uint16
	_pad       uint16
}

// KernelAttrManager manages the attribute graph in the shared constraint pages.
// Single instance (attrMgr), initialized once during boot.
type KernelAttrManager struct {
	baseVA uintptr // kernel VA of shared pages start
	basePA uintptr // PA of shared pages start

	// Region offsets (byte offsets from baseVA)
	nodeRegionOff     uint32
	edgeRegionOff     uint32
	bytecodeRegionOff uint32
	stringRegionOff   uint32
	collRegionOff     uint32
	trieRegionOff     uint32

	// Capacities
	nodeCapacity uint16
	trieCapacity uint16
	stringCap    uint16
	edgeCap      uint16

	// Bitmap allocators — one bit per slot, 1 = allocated
	nodeBitmap   [512]byte // 4096 bits = 4096 node slots
	stringBitmap [256]byte // 2048 bits = 2048 string slots
	trieBitmap   [1024]byte // 8192 bits = 8192 trie nodes

	// Bump allocators (byte offsets into their regions, relative to region start)
	edgeBumpOff      uint32 // next free byte in edge region
	bytecodeBumpOff  uint32 // next free byte in bytecode region
	collectionBumpOff uint32 // next free byte in collection region

	// Generation counter (also written to shared page header)
	generation uint64

	// Dirty walk generation (for diamond dedup)
	walkGen uint64

	// Query pattern registry (kernel-only, not in shared pages)
	queries    [MaxQueryPatterns]queryPattern
	queryCount uint16

	initialized bool
}

// attrMgr is the singleton kernel attribute manager.
var attrMgr KernelAttrManager

// InitKernelAttrManager initializes the kernel attribute manager from the
// shared constraint pages. Must be called after InitConstraintPages().
// Safe to call multiple times; only the first call initializes.
func InitKernelAttrManager() bool {
	if attrMgr.initialized {
		return true
	}

	va := kmem.GetConstraintPageKernelVA()
	if va == 0 {
		klog.Errf("[attr] InitKernelAttrManager: constraint pages not initialized\n")
		return false
	}

	pa := kmem.GetConstraintPagePA()

	// Read the header to discover region offsets.
	hdr := (*kmem.SharedPageHeader)(unsafe.Pointer(va))
	if hdr.Magic != kmem.ConstraintPageMagic || hdr.Version != kmem.ConstraintPageVersion {
		klog.Errf("[attr] InitKernelAttrManager: bad header magic/version\n")
		return false
	}

	attrMgr.baseVA = va
	attrMgr.basePA = pa
	attrMgr.nodeRegionOff = hdr.NodeRegionOff
	attrMgr.edgeRegionOff = hdr.EdgeRegionOff
	attrMgr.bytecodeRegionOff = hdr.BytecodeRegionOff
	attrMgr.stringRegionOff = hdr.StringRegionOff
	attrMgr.collRegionOff = hdr.CollRegionOff
	attrMgr.trieRegionOff = hdr.TrieRegionOff

	attrMgr.nodeCapacity = hdr.NodeCapacity
	attrMgr.trieCapacity = hdr.TrieCapacity
	attrMgr.stringCap = hdr.StringCapacity
	attrMgr.edgeCap = hdr.EdgeCapacity

	// Initialize the namespace trie root and top-level children.
	initTrie()

	// Initialize per-shepherd notification queues.
	initNotifyQueues()

	attrMgr.initialized = true

	return true
}

// node returns a writable pointer to the FlatAttrNode at the given slot index.
//
//go:nosplit
func (mgr *KernelAttrManager) node(slot uint16) *flat.FlatAttrNode {
	off := uintptr(mgr.nodeRegionOff) + uintptr(slot)*flat.FlatAttrNodeSize
	return (*flat.FlatAttrNode)(unsafe.Pointer(mgr.baseVA + off))
}

// allocNode allocates a free node slot from the bitmap.
// Returns slot index, or 0xFFFF if full.
//
//go:nosplit
func (mgr *KernelAttrManager) allocNode() uint16 {
	for byteIdx := 0; byteIdx < len(mgr.nodeBitmap); byteIdx++ {
		b := mgr.nodeBitmap[byteIdx]
		if b == 0xFF {
			continue // all 8 slots taken
		}
		for bit := uint(0); bit < 8; bit++ {
			if b&(1<<bit) == 0 {
				slot := uint16(byteIdx*8) + uint16(bit)
				if slot >= mgr.nodeCapacity {
					return 0xFFFF
				}
				mgr.nodeBitmap[byteIdx] |= 1 << bit
				// Zero the node
				n := mgr.node(slot)
				*n = flat.FlatAttrNode{}
				return slot
			}
		}
	}
	return 0xFFFF
}

// freeNode releases a node slot back to the bitmap.
//
//go:nosplit
func (mgr *KernelAttrManager) freeNode(slot uint16) {
	byteIdx := slot / 8
	bit := slot % 8
	mgr.nodeBitmap[byteIdx] &^= 1 << bit
}

// isNodeAllocated checks if a node slot is currently allocated.
//
//go:nosplit
func (mgr *KernelAttrManager) isNodeAllocated(slot uint16) bool {
	if slot >= mgr.nodeCapacity {
		return false
	}
	byteIdx := slot / 8
	bit := slot % 8
	return mgr.nodeBitmap[byteIdx]&(1<<bit) != 0
}

// allocString allocates a string slot (256 bytes each) and copies the string.
// Returns (byte offset from string region start, true) or (0, false) if full.
//
//go:nosplit
func (mgr *KernelAttrManager) allocString(s string) (uint32, bool) {
	if len(s) > flat.FlatStringMaxLen {
		return 0, false
	}
	for byteIdx := 0; byteIdx < len(mgr.stringBitmap); byteIdx++ {
		b := mgr.stringBitmap[byteIdx]
		if b == 0xFF {
			continue
		}
		for bit := uint(0); bit < 8; bit++ {
			if b&(1<<bit) == 0 {
				slot := uint16(byteIdx*8) + uint16(bit)
				if slot >= mgr.stringCap {
					return 0, false
				}
				mgr.stringBitmap[byteIdx] |= 1 << bit
				off := uint32(slot) * flat.FlatStringSlotSize
				// Write string data into shared page
				dst := mgr.baseVA + uintptr(mgr.stringRegionOff) + uintptr(off)
				for i := 0; i < len(s); i++ {
					*(*byte)(unsafe.Pointer(dst + uintptr(i))) = s[i]
				}
				// NUL-terminate
				*(*byte)(unsafe.Pointer(dst + uintptr(len(s)))) = 0
				return off, true
			}
		}
	}
	return 0, false
}

// freeString releases a string slot back to the bitmap.
// off is the byte offset from the string region start (same as FlatStrRef.RegionOffset).
//
//go:nosplit
func (mgr *KernelAttrManager) freeString(off uint32) {
	slot := uint16(off / flat.FlatStringSlotSize)
	if slot >= mgr.stringCap {
		return
	}
	byteIdx := int(slot / 8)
	bit := uint(slot % 8)
	mgr.stringBitmap[byteIdx] &^= 1 << bit
}

// allocEdges bump-allocates space for n uint16 edge entries.
// Returns (byte offset from edge region start, true) or (0, false) if full.
//
//go:nosplit
func (mgr *KernelAttrManager) allocEdges(n int) (uint32, bool) {
	needed := uint32(n) * 2 // uint16 = 2 bytes each
	if mgr.edgeBumpOff+needed > uint32(mgr.edgeCap)*2 {
		return 0, false
	}
	off := mgr.edgeBumpOff
	mgr.edgeBumpOff += needed
	return off, true
}

// readEdge reads a uint16 edge slot from the edge region.
//
//go:nosplit
func (mgr *KernelAttrManager) readEdge(regionOff uint32, index int) uint16 {
	addr := mgr.baseVA + uintptr(mgr.edgeRegionOff) + uintptr(regionOff) + uintptr(index)*2
	return *(*uint16)(unsafe.Pointer(addr))
}

// writeEdge writes a uint16 edge slot into the edge region.
//
//go:nosplit
func (mgr *KernelAttrManager) writeEdge(regionOff uint32, index int, val uint16) {
	addr := mgr.baseVA + uintptr(mgr.edgeRegionOff) + uintptr(regionOff) + uintptr(index)*2
	*(*uint16)(unsafe.Pointer(addr)) = val
}

// writeEdgeBlock allocates and writes a block of edge entries.
// Returns (byte offset from edge region start, true) or (0, false) if full.
//
//go:nosplit
func (mgr *KernelAttrManager) writeEdgeBlock(slots []uint16) (uint32, bool) {
	off, ok := mgr.allocEdges(len(slots))
	if !ok {
		return 0, false
	}
	for i, s := range slots {
		mgr.writeEdge(off, i, s)
	}
	return off, true
}

// bumpGeneration increments the shared page generation counter.
//
//go:nosplit
func (mgr *KernelAttrManager) bumpGeneration() {
	mgr.generation++
	hdr := (*kmem.SharedPageHeader)(unsafe.Pointer(mgr.baseVA))
	hdr.Generation = mgr.generation
}
