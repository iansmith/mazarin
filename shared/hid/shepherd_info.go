package hid

// ShepherdInfoEntry describes a single running shepherd, returned by the
// ShepherdInfo syscall. The struct is shared between kernel and userspace.
//
// Layout (208 bytes total):
//
//	offset  0: PID         int16
//	offset  2: ThreadCount int16
//	offset  4: PageCount   uint32
//	offset  8: FilenameLen uint8
//	offset  9: _pad0       [1]byte
//	offset 10: NameLen     int16
//	offset 12: _pad1       [4]byte
//	offset 16: Filename    [64]byte
//	offset 80: ThreadIDs   [32]int16
//	offset 144: Name       [64]byte
type ShepherdInfoEntry struct {
	PID         int16     // Unique shepherd identifier (kernel int16 SID)
	ThreadCount int16     // Number of live threads
	PageCount   uint32    // Number of 4KB pages mapped in this shepherd's address space
	FilenameLen uint8     // Length of Filename (bytes, without NUL)
	_pad0       [1]byte   // Alignment padding
	NameLen     int16     // Length of Name (bytes, without NUL)
	_pad1       [4]byte   // Alignment padding
	Filename    [64]byte  // Launch filename (e.g., "/rachel.elf"), not NUL-terminated
	ThreadIDs   [32]int16 // TIDs of threads belonging to this shepherd (-1 = unused)
	Name        [64]byte  // TOML launch name (e.g., "rachel"), not NUL-terminated
}
