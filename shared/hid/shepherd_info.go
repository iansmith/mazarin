package hid

// ShepherdInfoEntry describes a single running shepherd, returned by the
// ShepherdInfo syscall. The struct is shared between kernel and userspace.
type ShepherdInfoEntry struct {
	PID         int16    // Unique shepherd identifier
	ThreadCount int16    // Number of live threads
	PageCount   uint32   // Number of 4KB pages mapped in this shepherd's address space
	FilenameLen uint8    // Length of Filename (bytes, without NUL)
	_pad        [7]byte  // Alignment padding
	Filename    [64]byte // Launch filename (e.g., "/rachel.elf"), not NUL-terminated
	ThreadIDs   [32]int16 // TIDs of threads belonging to this shepherd (-1 = unused)
}
