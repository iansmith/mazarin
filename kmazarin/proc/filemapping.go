package proc

// FileMapping records a file-backed mmap region. When a page fault occurs
// in [StartVA, StartVA+Length), the kernel reads file data from fd at
// FileOffset + (faultVA - StartVA) and maps it read-only.
type FileMapping struct {
	StartVA    uint64 // Start of the VA range
	Length     uint64 // Length in bytes (page-aligned)
	CallerSID  int16  // Shepherd that owns the fd
	FD         int32  // fd in that shepherd's FDT
	FileOffset uint64 // Byte offset in file where mapping starts
	InUse      bool
}

// MaxFileMappings is the maximum number of file-backed mmap regions per shepherd.
const MaxFileMappings = 64

// FindFileMappingByVA returns the FileMapping containing va, or nil if none.
//
//go:nosplit
func (p *Shepherd) FindFileMappingByVA(va uint64) *FileMapping {
	for i := 0; i < MaxFileMappings; i++ {
		fm := &p.FileMappings[i]
		if fm.InUse && va >= fm.StartVA && va < fm.StartVA+fm.Length {
			return fm
		}
	}
	return nil
}

// AddFileMapping records a new file-backed mmap region.
// Returns true on success, false if no free slots.
//
//go:nosplit
func (p *Shepherd) AddFileMapping(startVA, length uint64, callerSID int16, fd int32, fileOffset uint64) bool {
	for i := 0; i < MaxFileMappings; i++ {
		fm := &p.FileMappings[i]
		if !fm.InUse {
			fm.StartVA = startVA
			fm.Length = length
			fm.CallerSID = callerSID
			fm.FD = fd
			fm.FileOffset = fileOffset
			fm.InUse = true
			return true
		}
	}
	return false
}

// RemoveFileMapping removes any file-backed mappings overlapping [startVA, startVA+length).
//
//go:nosplit
func (p *Shepherd) RemoveFileMapping(startVA, length uint64) {
	end := startVA + length
	for i := 0; i < MaxFileMappings; i++ {
		fm := &p.FileMappings[i]
		if !fm.InUse {
			continue
		}
		fmEnd := fm.StartVA + fm.Length
		// Check for overlap
		if fm.StartVA < end && fmEnd > startVA {
			fm.InUse = false
		}
	}
}
