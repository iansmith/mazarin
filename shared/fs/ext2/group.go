package ext2

import "encoding/binary"

// GroupDescSize is the on-disk size of a block group descriptor.
const GroupDescSize = 32

// GroupDesc represents a block group descriptor (32 bytes).
// There is one per block group, stored in the block group descriptor table
// immediately after the superblock (or its backup).
type GroupDesc struct {
	BlockBitmap     uint32 // Block number of block bitmap
	InodeBitmap     uint32 // Block number of inode bitmap
	InodeTable      uint32 // Block number of first inode table block
	FreeBlocksCount uint16 // Number of free blocks in this group
	FreeInodesCount uint16 // Number of free inodes in this group
	UsedDirsCount   uint16 // Number of directories in this group
	Pad             uint16 // Padding to 4-byte alignment
	Reserved        [12]byte
}

// Marshal serializes the group descriptor to a 32-byte buffer.
func (gd *GroupDesc) Marshal() [GroupDescSize]byte {
	var buf [GroupDescSize]byte
	le := binary.LittleEndian

	le.PutUint32(buf[0:4], gd.BlockBitmap)
	le.PutUint32(buf[4:8], gd.InodeBitmap)
	le.PutUint32(buf[8:12], gd.InodeTable)
	le.PutUint16(buf[12:14], gd.FreeBlocksCount)
	le.PutUint16(buf[14:16], gd.FreeInodesCount)
	le.PutUint16(buf[16:18], gd.UsedDirsCount)
	le.PutUint16(buf[18:20], gd.Pad)
	copy(buf[20:32], gd.Reserved[:])

	return buf
}

// UnmarshalGroupDesc reads a group descriptor from a buffer.
func UnmarshalGroupDesc(data []byte) (*GroupDesc, error) {
	if len(data) < GroupDescSize {
		return nil, ErrCorrupted
	}

	le := binary.LittleEndian
	gd := &GroupDesc{}

	gd.BlockBitmap = le.Uint32(data[0:4])
	gd.InodeBitmap = le.Uint32(data[4:8])
	gd.InodeTable = le.Uint32(data[8:12])
	gd.FreeBlocksCount = le.Uint16(data[12:14])
	gd.FreeInodesCount = le.Uint16(data[14:16])
	gd.UsedDirsCount = le.Uint16(data[16:18])
	gd.Pad = le.Uint16(data[18:20])
	copy(gd.Reserved[:], data[20:32])

	return gd, nil
}
