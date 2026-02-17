package kmem

// userMmapStart for RISC-V: 256MB, above typical ELF VA range.
// Must match ksyscall/mmap_addr_riscv64.go.
const userMmapStart = 0x10000000
