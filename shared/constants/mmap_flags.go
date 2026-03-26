package constants

// MAZARIN_CONTIGUOUS is an mmap flag that requests physically contiguous pages.
// When set, the kernel allocates a power-of-two block from the buddy allocator,
// pre-maps all PTEs (no demand paging), and registers the pages as a DMA clump
// for the calling shepherd. The VA can be used directly with BlockSubmit.
//
// Bit 21, above all standard Linux MAP_* flags (MAP_HUGETLB is bit 18).
const MAZARIN_CONTIGUOUS = 0x200000
