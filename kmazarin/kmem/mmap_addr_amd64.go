package kmem

// userMmapStart for x86_64: 0xC000000000 (PML4[1]), matching Go's standard arena hint.
// Must match ksyscall/mmap_addr_amd64.go.
const userMmapStart = 0xC000000000
