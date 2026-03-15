// constraint.go — Constraint VM shared page allocation.
//
// Allocates a contiguous block of physical pages from the unified pool.
// These pages are mapped read-only into every priest's address space at
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
	ConstraintPageCount = 128                       // 128 × 4KB = 512KB
	ConstraintTotalSize = ConstraintPageCount * 4096 // 512KB
)

// Page header magic for constraint shared pages.
const ConstraintPageMagic = 0x4D415A46 // "MAZF"
const ConstraintPageVersion = 1

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

	// Order 7 = 128 pages = 512KB
	pa := BuddyAllocTyped(7, PageConstraintShared, 0)
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

	// Write magic + version at the start of the page header.
	// Layout: [magic:uint32][version:uint32]
	hdr := (*[2]uint32)(unsafe.Pointer(va))
	hdr[0] = ConstraintPageMagic
	hdr[1] = ConstraintPageVersion

	constraintState.pa = pa
	atomic.StoreUint32(&constraintState.initialized, 1)

	serial.RawUARTPuts("[kmem] Constraint pages allocated at PA=0x")
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
// current priest's address space at the fixed VA.
// Must be called after TTBR0 has been switched to the priest's page table.
//
//go:nosplit
func MapUserConstraintPages() bool {
	// Lazy init: allocate on first priest launch.
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
