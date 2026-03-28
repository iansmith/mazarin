package ext2

import "encoding/binary"

// DirEntryHeaderSize is the fixed-size portion of a directory entry (before the name).
const DirEntryHeaderSize = 8

// MaxNameLen is the maximum filename length in ext2.
const MaxNameLen = 255

// DirEntry represents an ext2 directory entry.
// On disk, entries are variable-length: 8-byte header + name, padded
// to 4-byte alignment via RecLen. The last entry in a block has RecLen
// extended to fill the remaining space.
type DirEntry struct {
	Inode    uint32 // Inode number (0 = unused/deleted entry)
	RecLen   uint16 // Total size of this entry (including padding)
	NameLen  uint8  // Length of the name
	FileType uint8  // File type (FTFile, FTDir, etc.) — requires FeatureIncompatFileType
	Name     string // File name (NOT null-terminated on disk)
}

// DirEntrySize returns the actual size this entry occupies on disk,
// which is always RecLen (padded to fill space or to 4-byte alignment).
func (de *DirEntry) DirEntrySize() uint16 {
	return de.RecLen
}

// DirEntryRealSize returns the minimum size needed for this entry
// (header + name, rounded up to 4-byte alignment).
func DirEntryRealSize(nameLen int) uint16 {
	size := uint16(DirEntryHeaderSize + nameLen)
	// Round up to 4-byte alignment
	if size%4 != 0 {
		size += 4 - (size % 4)
	}
	return size
}

// MarshalDirEntry writes a directory entry into buf at the given offset.
// Returns the number of bytes written (always de.RecLen).
// The caller must ensure buf has enough space.
func MarshalDirEntry(buf []byte, de *DirEntry) int {
	le := binary.LittleEndian

	le.PutUint32(buf[0:4], de.Inode)
	le.PutUint16(buf[4:6], de.RecLen)
	buf[6] = de.NameLen
	buf[7] = de.FileType

	copy(buf[DirEntryHeaderSize:DirEntryHeaderSize+int(de.NameLen)], de.Name)

	// Zero any padding between end of name and RecLen
	for i := DirEntryHeaderSize + int(de.NameLen); i < int(de.RecLen); i++ {
		buf[i] = 0
	}

	return int(de.RecLen)
}

// UnmarshalDirEntry reads a directory entry from buf.
// Returns the entry and the number of bytes consumed (RecLen).
func UnmarshalDirEntry(buf []byte) (*DirEntry, int, error) {
	if len(buf) < DirEntryHeaderSize {
		return nil, 0, ErrCorrupted
	}

	le := binary.LittleEndian
	de := &DirEntry{}

	de.Inode = le.Uint32(buf[0:4])
	de.RecLen = le.Uint16(buf[4:6])
	de.NameLen = buf[6]
	de.FileType = buf[7]

	if de.RecLen < DirEntryHeaderSize {
		return nil, 0, ErrCorrupted
	}
	if int(de.RecLen) > len(buf) {
		return nil, 0, ErrCorrupted
	}
	if int(DirEntryHeaderSize+de.NameLen) > len(buf) {
		return nil, 0, ErrCorrupted
	}

	de.Name = string(buf[DirEntryHeaderSize : DirEntryHeaderSize+int(de.NameLen)])

	return de, int(de.RecLen), nil
}

// NewDirEntry creates a DirEntry with the correct NameLen and RecLen.
// RecLen is set to the minimum aligned size. The caller may expand
// RecLen if this is the last entry in a block.
func NewDirEntry(inode uint32, name string, fileType uint8) *DirEntry {
	nameLen := len(name)
	if nameLen > MaxNameLen {
		nameLen = MaxNameLen
		name = name[:MaxNameLen]
	}
	return &DirEntry{
		Inode:    inode,
		RecLen:   DirEntryRealSize(nameLen),
		NameLen:  uint8(nameLen),
		FileType: fileType,
		Name:     name,
	}
}
