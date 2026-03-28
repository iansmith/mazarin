package ext2

import "encoding/binary"

// InodeSize128 is the standard ext2 inode size.
const InodeSize128 = 128

// Inode represents an ext2 inode (128 bytes for standard ext2).
//
// Block pointers: Blocks[0..11] are direct (point to data blocks),
// Blocks[12] is single-indirect, Blocks[13] is double-indirect,
// Blocks[14] is triple-indirect.
//
// For files <= 48KB (with 4KB blocks), only direct pointers are needed.
type Inode struct {
	Mode       uint16     // File type and permissions
	UID        uint16     // Owner user ID
	Size       uint32     // File size in bytes (lower 32 bits)
	ATime      uint32     // Last access time (Unix timestamp)
	CTime      uint32     // Creation/change time (Unix timestamp)
	MTime      uint32     // Last modification time (Unix timestamp)
	DTime      uint32     // Deletion time (0 if not deleted)
	GID        uint16     // Owner group ID
	LinksCount uint16     // Hard link count
	Blocks     uint32     // Number of 512-byte sectors allocated
	Flags      uint32     // Inode flags
	OSD1       uint32     // OS-dependent value 1
	Block      [15]uint32 // Block pointers
	Generation uint32     // File version (for NFS)
	FileACL    uint32     // File ACL block
	DirACL     uint32     // Directory ACL (or upper 32 bits of size for large files)
	FAddr      uint32     // Fragment address (obsolete)
	OSD2       [12]byte   // OS-dependent value 2
}

// Direct block pointer indices
const (
	NDirect         = 12 // Number of direct block pointers
	IndirectBlock    = 12 // Index of single-indirect block pointer
	DblIndirectBlock = 13 // Index of double-indirect block pointer
	TplIndirectBlock = 14 // Index of triple-indirect block pointer
)

// IsDir returns true if this inode is a directory.
func (in *Inode) IsDir() bool {
	return in.Mode&TypeMask == TypeDir
}

// IsFile returns true if this inode is a regular file.
func (in *Inode) IsFile() bool {
	return in.Mode&TypeMask == TypeFile
}

// IsSymlink returns true if this inode is a symbolic link.
func (in *Inode) IsSymlink() bool {
	return in.Mode&TypeMask == TypeLink
}

// Permissions returns the permission bits (lower 12 bits of mode).
func (in *Inode) Permissions() uint16 {
	return in.Mode & 0x0FFF
}

// FileType returns the file type bits (upper 4 bits of mode).
func (in *Inode) FileType() uint16 {
	return in.Mode & TypeMask
}

// Marshal serializes the inode to a 128-byte buffer.
func (in *Inode) Marshal() [InodeSize128]byte {
	var buf [InodeSize128]byte
	le := binary.LittleEndian

	le.PutUint16(buf[0:2], in.Mode)
	le.PutUint16(buf[2:4], in.UID)
	le.PutUint32(buf[4:8], in.Size)
	le.PutUint32(buf[8:12], in.ATime)
	le.PutUint32(buf[12:16], in.CTime)
	le.PutUint32(buf[16:20], in.MTime)
	le.PutUint32(buf[20:24], in.DTime)
	le.PutUint16(buf[24:26], in.GID)
	le.PutUint16(buf[26:28], in.LinksCount)
	le.PutUint32(buf[28:32], in.Blocks)
	le.PutUint32(buf[32:36], in.Flags)
	le.PutUint32(buf[36:40], in.OSD1)
	for i := 0; i < 15; i++ {
		le.PutUint32(buf[40+i*4:44+i*4], in.Block[i])
	}
	le.PutUint32(buf[100:104], in.Generation)
	le.PutUint32(buf[104:108], in.FileACL)
	le.PutUint32(buf[108:112], in.DirACL)
	le.PutUint32(buf[112:116], in.FAddr)
	copy(buf[116:128], in.OSD2[:])

	return buf
}

// UnmarshalInode reads an inode from a buffer.
func UnmarshalInode(data []byte) (*Inode, error) {
	if len(data) < InodeSize128 {
		return nil, ErrInvalidInode
	}

	le := binary.LittleEndian
	in := &Inode{}

	in.Mode = le.Uint16(data[0:2])
	in.UID = le.Uint16(data[2:4])
	in.Size = le.Uint32(data[4:8])
	in.ATime = le.Uint32(data[8:12])
	in.CTime = le.Uint32(data[12:16])
	in.MTime = le.Uint32(data[16:20])
	in.DTime = le.Uint32(data[20:24])
	in.GID = le.Uint16(data[24:26])
	in.LinksCount = le.Uint16(data[26:28])
	in.Blocks = le.Uint32(data[28:32])
	in.Flags = le.Uint32(data[32:36])
	in.OSD1 = le.Uint32(data[36:40])
	for i := 0; i < 15; i++ {
		in.Block[i] = le.Uint32(data[40+i*4 : 44+i*4])
	}
	in.Generation = le.Uint32(data[100:104])
	in.FileACL = le.Uint32(data[104:108])
	in.DirACL = le.Uint32(data[108:112])
	in.FAddr = le.Uint32(data[112:116])
	copy(in.OSD2[:], data[116:128])

	return in, nil
}
