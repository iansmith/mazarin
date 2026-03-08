package hid

// PriestInfoEntry describes a single running priest, returned by the
// PriestInfo syscall. The struct is shared between kernel and userspace.
type PriestInfoEntry struct {
	PID         int16    // Unique priest identifier
	ThreadCount int16    // Number of live threads
	PageCount   uint32   // Number of 4KB pages mapped in this priest's address space
	FilenameLen uint8    // Length of Filename (bytes, without NUL)
	_pad        [7]byte  // Alignment padding
	Filename    [64]byte // Launch filename (e.g., "/dapope.elf"), not NUL-terminated
	ThreadIDs   [32]int16 // TIDs of threads belonging to this priest (-1 = unused)
}
