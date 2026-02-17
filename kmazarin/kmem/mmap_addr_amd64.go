package kmem

// userMmapStart for x86_64: 2GB, above kmazarin code at PDPT[1] (0x40000000-0x7FFFFFFF).
// Must match ksyscall/mmap_addr_amd64.go.
const userMmapStart = 0x80000000
