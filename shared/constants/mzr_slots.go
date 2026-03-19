package constants

// .mzr address slots — fixed virtual addresses for non-PIE modules on RISC-V.
//
// The Go compiler for RISC-V does not support -buildmode=pie (position-independent
// executables). On ARM64 and AMD64, .maz modules are PIE (ET_DYN) binaries that
// can be loaded at any userspace address. On RISC-V, we must instead produce
// ET_EXEC binaries linked at a specific address using -ldflags="-T <address>".
// These are called .mzr files.
//
// Each .mzr occupies a fixed 32MB "slot" in the shepherd's virtual address space.
// The user must coordinate slot assignments at build time to avoid overlaps —
// two .mzr modules compiled for the same slot cannot be loaded into the same shepherd.
//
// Slot layout (8 slots, 32MB each, starting at 0x30000000):
//
//	Slot 0: 0x30000000 – 0x31FFFFFF  (fs filesystem module)
//	Slot 1: 0x32000000 – 0x33FFFFFF  (helloworld)
//	Slot 2: 0x34000000 – 0x35FFFFFF  (reserved)
//	Slot 3: 0x36000000 – 0x37FFFFFF  (reserved)
//	Slot 4: 0x38000000 – 0x39FFFFFF  (reserved)
//	Slot 5: 0x3A000000 – 0x3BFFFFFF  (reserved)
//	Slot 6: 0x3C000000 – 0x3DFFFFFF  (reserved)
//	Slot 7: 0x3E000000 – 0x3FFFFFFF  (reserved)
//
// To determine which slot an .mzr was compiled for, examine its ELF entry point:
//
//	slot = (entryPoint - MzrSlotBase) / MzrSlotSpacing
//
// The build system assigns slots via the -T flag:
//
//	-T 0x30000000  →  slot 0 (fs.mzr)
//	-T 0x32000000  →  slot 1 (helloworld.mzr)
const (
	MzrSlotBase    = 0x30000000 // Start of the .mzr slot region
	MzrSlotSpacing = 0x02000000 // 32MB between slots
	MzrSlotCount   = 8          // Number of available slots (0–7)

	MzrSlot0 = MzrSlotBase + 0*MzrSlotSpacing // 0x30000000 — fs
	MzrSlot1 = MzrSlotBase + 1*MzrSlotSpacing // 0x32000000 — helloworld
	MzrSlot2 = MzrSlotBase + 2*MzrSlotSpacing // 0x34000000 — reserved
	MzrSlot3 = MzrSlotBase + 3*MzrSlotSpacing // 0x36000000 — reserved
	MzrSlot4 = MzrSlotBase + 4*MzrSlotSpacing // 0x38000000 — reserved
	MzrSlot5 = MzrSlotBase + 5*MzrSlotSpacing // 0x3A000000 — reserved
	MzrSlot6 = MzrSlotBase + 6*MzrSlotSpacing // 0x3C000000 — reserved
	MzrSlot7 = MzrSlotBase + 7*MzrSlotSpacing // 0x3E000000 — reserved
)

// MzrSlotFromAddr returns the slot number (0–7) for a given address within
// the .mzr slot region. Returns -1 if the address is outside the slot region.
func MzrSlotFromAddr(addr uint64) int {
	if addr < MzrSlotBase {
		return -1
	}
	slot := int((addr - MzrSlotBase) / MzrSlotSpacing)
	if slot >= MzrSlotCount {
		return -1
	}
	return slot
}
