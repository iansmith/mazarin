package kmem

// userMmapStart for ARM64: 256MB, above typical ELF VA range.
// Must match ksyscall/mmap_addr_arm64.go.
const userMmapStart = 0x10000000
