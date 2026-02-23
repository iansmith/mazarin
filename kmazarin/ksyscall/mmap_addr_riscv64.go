package ksyscall

// userMmapStart for RISC-V: 0xC000000000 (L3[1]), matching Go's standard arena hint.
//
// L3[1] is left empty in initProcessL0 (only L3[256-511] are copied for kernel
// mappings), so demand paging at this address works correctly.
const userMmapStart = 0xC000000000
