package kmem

// userMmapStart for ARM64: 0xC000000000, matching Go's standard arena hint.
// Must match ksyscall/mmap_addr_arm64.go.
const userMmapStart = 0xC000000000
