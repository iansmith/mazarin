package ksyscall

// userMmapStart for RISC-V: 256MB, above typical ELF VA range.
// RISC-V has separate kernel/user address spaces (Sv48 upper/lower half),
// so no conflict with kmazarin code.
const userMmapStart = 0x10000000
