package kmem

// userMmapStart for RISC-V: 0xC000000000 (L3[1]), matching Go's standard arena hint.
// Must match ksyscall/mmap_addr_riscv64.go.
const userMmapStart = 0xC000000000
