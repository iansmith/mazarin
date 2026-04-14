package ksyscall

// userMmapStart for ARM64: 0x200000000000.
//
// This MUST NOT overlap Go's heap arena hints (which start at 0xC000000000).
// Go's runtime uses MAP_FIXED to claim arena chunks; if the bump allocator
// hands out VAs in that range, Go's MAP_FIXED will overwrite file-backed
// mmap PTEs with anonymous pages, causing data corruption.
const userMmapStart = 0x200000000000

// ipcDataVAStart is the starting VA for cross-shepherd IPC allocations.
// Separate from userMmapStart to avoid collisions with Go's heap allocator.
const ipcDataVAStart = 0x0000500000000000
