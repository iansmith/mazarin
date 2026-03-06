package constants

// .mzr address slots — fixed virtual addresses for non-PIE modules.
// Each .mzr is built with -T <slot address>. Slots are spaced 32MB apart
// starting at 0x30000000, well above the host priest's text/data/heap.
const (
	MzrSlot0       = 0x30000000 // fs (filesystem)
	MzrSlot1       = 0x32000000 // reserved
	MzrSlot2       = 0x34000000 // reserved
	MzrSlot3       = 0x36000000 // reserved
	MzrSlotSpacing = 0x02000000 // 32MB between slots
)
