package kmem

// userMmapStart for ARM64: 0x200000000000.
// Must match ksyscall/mmap_addr_arm64.go.
const userMmapStart = 0x200000000000
