package ksyscall

// userMmapStart for ARM64: 0xC000000000, matching Go's standard arena hint.
//
// ARM64 has separate kernel/user address spaces (TTBR0/TTBR1), so no conflict
// with kmazarin code. This address falls in TTBR0 space and is left empty by
// initProcessL0 for demand paging.
const userMmapStart = 0xC000000000
