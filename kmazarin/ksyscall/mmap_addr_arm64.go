package ksyscall

// userMmapStart for ARM64: 256MB, above typical ELF VA range.
// ARM64 has separate kernel/user address spaces (TTBR0/TTBR1), so no conflict
// with kmazarin code.
const userMmapStart = 0x10000000
