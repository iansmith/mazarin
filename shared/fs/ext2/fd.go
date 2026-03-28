package ext2

// OpenMode describes how a file descriptor was opened.
type OpenMode uint8

const (
	ModeRead  OpenMode = 1
	ModeWrite OpenMode = 2
	ModeRW    OpenMode = ModeRead | ModeWrite
)

// FD is a file descriptor tracking an open file's position and cached metadata.
// The kernel or syscall handler maintains a table of these, indexed by fd number.
type FD struct {
	Inum   uint32   // inode number on disk
	Offset int64    // current byte position (updated by read/write/seek)
	Mode   OpenMode // how the file was opened
	Size   uint32   // cached file size (updated on write/truncate)
	FType  uint8    // cached file type: FTFile, FTDir, etc.
}

// CanRead returns true if this fd was opened for reading.
func (fd *FD) CanRead() bool {
	return fd.Mode&ModeRead != 0
}

// CanWrite returns true if this fd was opened for writing.
func (fd *FD) CanWrite() bool {
	return fd.Mode&ModeWrite != 0
}

// IsDir returns true if this fd refers to a directory.
func (fd *FD) IsDir() bool {
	return fd.FType == FTDir
}

// Remaining returns the number of bytes from the current offset to EOF.
func (fd *FD) Remaining() int64 {
	r := int64(fd.Size) - fd.Offset
	if r < 0 {
		return 0
	}
	return r
}
