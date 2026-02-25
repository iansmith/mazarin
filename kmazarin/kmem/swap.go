// swap.go - Page swap-out/swap-in stubs (Stage 5)
//
// SwapOutPage and SwapInPage are stubs that log their intent but return
// ErrSwapNotImplemented. They define the correct function signatures and
// parameter contracts for the eventual swap I/O implementation.
//
// When actual swap I/O is implemented, these functions will:
//   SwapOutPage: write page contents to swap device, update PTE to swap slot
//   SwapInPage:  read page from swap device, allocate frame, remap at fault VA
//
// The swap slot encoding in PTEs (for not-present entries carrying a block
// reference) is not yet defined — that encoding will be added in a future
// stage alongside the block device swap target selection.
//
// Design reference: design/MEMORY_OVERHAUL.md Stage 5

package kmem

import (
	"errors"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// ErrSwapNotImplemented is returned by swap stubs until swap I/O is implemented.
var ErrSwapNotImplemented = errors.New("swap not implemented")

// SwapSlot identifies a location in the swap device (block offset).
type SwapSlot struct {
	DeviceID uint8  // Which block device (0 = first block device)
	BlockNum uint64 // Block number on device
}

// SwapOutPage writes the contents of the physical page at pa to the swap device
// and marks the PTE as not-present (encoding the swap slot in the PTE).
//
// Parameters:
//   - pa:      physical address of the page to swap out
//   - desc:    the page's PageDescriptor (type, owner, dirty flag); may be nil
//   - va:      virtual address where the page is mapped in the priest's space
//   - priestID: the owning priest (for TLB invalidation after PTE update)
//
// Returns the swap slot where the page was written, or ErrSwapNotImplemented.
func SwapOutPage(pa uintptr, desc *PageDescriptor, va uintptr, priestID proc.PriestId) (SwapSlot, error) {
	serial.RawUARTPuts("[swap] Would swap out PA=0x")
	serial.RawUARTHex64(uint64(pa))
	serial.RawUARTPuts(" VA=0x")
	serial.RawUARTHex64(uint64(va))
	serial.RawUARTPuts(" priest=")
	serial.RawUARTHex64(uint64(priestID))
	if desc != nil {
		serial.RawUARTPuts(" type=")
		typeName := desc.Type.String()
		for i := 0; i < len(typeName); i++ {
			serial.PollWrite(typeName[i])
		}
		serial.RawUARTPuts(" dirty=")
		if desc.Flags&PD_DIRTY != 0 {
			serial.RawUARTPuts("true")
		} else {
			serial.RawUARTPuts("false")
		}
	}
	serial.RawUARTPuts("\r\n")
	return SwapSlot{}, ErrSwapNotImplemented
}

// SwapInPage reads a page from the swap device and maps it at the given VA.
// Called from the page fault handler when a not-present PTE carries a swap slot
// reference (future: when isSwapPTE(pte) returns true in HandleUserPageFault).
//
// Parameters:
//   - va:      the faulting virtual address (page-aligned)
//   - slot:    the swap slot to read from
//   - priestID: the owning priest
//
// Returns the new physical address where the page was loaded, or
// ErrSwapNotImplemented.
func SwapInPage(va uintptr, slot SwapSlot, priestID proc.PriestId) (uintptr, error) {
	serial.RawUARTPuts("[swap] Would swap in VA=0x")
	serial.RawUARTHex64(uint64(va))
	serial.RawUARTPuts(" device=")
	serial.RawUARTHex64(uint64(slot.DeviceID))
	serial.RawUARTPuts(" block=")
	serial.RawUARTHex64(slot.BlockNum)
	serial.RawUARTPuts(" priest=")
	serial.RawUARTHex64(uint64(priestID))
	serial.RawUARTPuts("\r\n")
	return 0, ErrSwapNotImplemented
}
