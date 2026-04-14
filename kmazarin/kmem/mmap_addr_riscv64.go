package kmem

// userMmapStart for RISC-V: 0x200000000000.
// Must match ksyscall/mmap_addr_riscv64.go.
const userMmapStart = 0x200000000000
