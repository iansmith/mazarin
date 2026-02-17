package ksyscall

// userMmapStart for x86_64: start at 2GB (0x80000000), above kmazarin code.
//
// On x86_64, kmazarin code occupies PML4[0] PDPT[1] (VA 0x40000000-0x7FFFFFFF)
// in the same address space as userspace. Starting mmap at 0x80000000 (PDPT[2])
// ensures user allocations don't conflict with kernel code page table entries.
const userMmapStart = 0x80000000
