package ksyscall

// userMmapStart for x86_64: 0xC000000000 (PML4[1]), matching Go's standard arena hint.
//
// PML4[1-255] are zeroed in initProcessL0, so demand paging at this address works
// out of the box. ELF segments (~0x400000), thread stacks (0x7FFF00000000), and
// MAP_FIXED mappings all live below this address in the MAP_FIXED region.
const userMmapStart = 0xC000000000
