package kmem

// userMmapStart for x86_64: 0x200000000000.
// Must match ksyscall/mmap_addr_amd64.go.
const userMmapStart = 0x200000000000
