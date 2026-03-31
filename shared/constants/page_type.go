package constants

// Userspace page type codes for SysAllocPages.
// These are passed from userspace to the kernel, which maps them to internal
// kmem.PageType values. Keep these stable — they are part of the syscall ABI.
const (
	// UserPageIPC is for IPC ring buffers and mailbox shared pages.
	UserPageIPC = 1
	// UserPageFontCache is for font glyph cache pages.
	UserPageFontCache = 2
	// UserPageShared is for general-purpose shared memory.
	UserPageShared = 3
	// UserPageRamdisk is for ramdisk backing store (off-heap, shepherd-owned).
	UserPageRamdisk = 4
)
