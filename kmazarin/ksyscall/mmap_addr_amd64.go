package ksyscall

// userMmapStart for x86_64: 0x200000000000.
//
// This MUST NOT overlap Go's heap arena hints (which start at 0xC000000000).
// Go's runtime uses MAP_FIXED to claim arena chunks; if the bump allocator
// hands out VAs in that range, Go's MAP_FIXED will overwrite file-backed
// mmap PTEs with anonymous pages, causing data corruption (e.g. badger
// memtable reads garbage instead of zeros).
//
// Go's MAP_FIXED calls go through the MAP_FIXED path in SyscallMmap
// (not the bump allocator), so they still work at 0xC000000000.
const userMmapStart = 0x200000000000

// ipcDataVAStart is the starting VA for cross-shepherd IPC allocations
// (data pages mapped into a target shepherd by the kernel for IPC, mmap
// page fill, mailbox, shared pages, etc.). This MUST be separate from
// userMmapStart because Go's heap allocator also starts at 0xC000000000
// and its MAP_FIXED arena management can create span gaps that allow the
// IPC bump allocator to hand out VAs that Go later overwrites.
const ipcDataVAStart = 0x0000500000000000
