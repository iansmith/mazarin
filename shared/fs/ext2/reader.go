package ext2

import (
	"encoding/binary"

	"mazzy/shared/blockdev"
)

// ErrEndOfFile indicates the end of a file has been reached.
var ErrEndOfFile = &Ext2Error{"end of file"}

// FileSystem represents a mounted ext2 filesystem.
type FileSystem struct {
	device    blockdev.BlockDevice
	sb        Superblock
	groups    []GroupDesc
	blockSize uint32
	numGroups uint32
	// Scratch buffer for partial-block reads (sized to max device block size)
	sectorBuf [4096]byte
	// Write support (populated by MountRW)
	writable     bool
	blockBitmaps [][]byte // one bitmap per group
	inodeBitmaps [][]byte // one bitmap per group
}

// Mount mounts an ext2 filesystem from a block device (read-only).
func Mount(device blockdev.BlockDevice) (*FileSystem, error) {
	fs := &FileSystem{device: device}
	if err := fs.init(); err != nil {
		return nil, err
	}
	return fs, nil
}

// MountRW mounts an ext2 filesystem with read-write support.
// This additionally loads all block and inode bitmaps into memory.
func MountRW(device blockdev.BlockDevice) (*FileSystem, error) {
	fs := &FileSystem{device: device}
	if err := fs.init(); err != nil {
		return nil, err
	}
	if err := fs.loadBitmaps(); err != nil {
		return nil, err
	}
	fs.writable = true
	return fs, nil
}

// loadBitmaps reads all block and inode bitmaps into memory.
func (fs *FileSystem) loadBitmaps() error {
	fs.blockBitmaps = make([][]byte, fs.numGroups)
	fs.inodeBitmaps = make([][]byte, fs.numGroups)
	for g := uint32(0); g < fs.numGroups; g++ {
		bm := make([]byte, fs.blockSize)
		if err := fs.readBlock(fs.groups[g].BlockBitmap, bm); err != nil {
			return err
		}
		fs.blockBitmaps[g] = bm

		im := make([]byte, fs.blockSize)
		if err := fs.readBlock(fs.groups[g].InodeBitmap, im); err != nil {
			return err
		}
		fs.inodeBitmaps[g] = im
	}
	return nil
}

// init reads the superblock and group descriptor table.
func (fs *FileSystem) init() error {
	// Read superblock (at byte offset 1024, always)
	var sbBuf [SuperblockSize]byte
	if err := fs.readBytes(SuperblockOffset, sbBuf[:]); err != nil {
		return ErrReadFailed
	}

	sb, err := UnmarshalSuperblock(sbBuf[:])
	if err != nil {
		return err
	}
	fs.sb = *sb
	fs.blockSize = BlockSize(sb.LogBlockSize)
	fs.numGroups = (sb.BlocksCount + sb.BlocksPerGroup - 1) / sb.BlocksPerGroup

	// Read group descriptor table (starts at the block after the superblock)
	// For 4K blocks: superblock is in block 0 (bytes 0-4095), GDT is in block 1
	// For 1K blocks: superblock is in block 1 (bytes 1024-2047), GDT starts at block 2
	gdtBlock := fs.sb.FirstDataBlock + 1
	gdtBytes := fs.numGroups * GroupDescSize
	gdtBuf := make([]byte, gdtBytes)
	if err := fs.readBytes(int64(gdtBlock)*int64(fs.blockSize), gdtBuf); err != nil {
		return ErrReadFailed
	}

	fs.groups = make([]GroupDesc, fs.numGroups)
	for i := uint32(0); i < fs.numGroups; i++ {
		offset := i * GroupDescSize
		gd, err := UnmarshalGroupDesc(gdtBuf[offset : offset+GroupDescSize])
		if err != nil {
			return err
		}
		fs.groups[i] = *gd
	}

	return nil
}

// readBytes reads len(buf) bytes from the block device starting at the given byte offset.
func (fs *FileSystem) readBytes(offset int64, buf []byte) error {
	devBlockSize := int64(fs.device.BlockSize())
	for len(buf) > 0 {
		lba := uint64(offset / devBlockSize)
		off := int(offset % devBlockSize)

		if off == 0 && len(buf) >= int(devBlockSize) {
			// Aligned full-sector read
			if err := fs.device.ReadBlock(lba, buf[:devBlockSize]); err != nil {
				return err
			}
			buf = buf[devBlockSize:]
			offset += devBlockSize
		} else {
			// Partial sector — read into scratch, copy out
			if err := fs.device.ReadBlock(lba, fs.sectorBuf[:]); err != nil {
				return err
			}
			n := int(devBlockSize) - off
			if n > len(buf) {
				n = len(buf)
			}
			copy(buf[:n], fs.sectorBuf[off:off+n])
			buf = buf[n:]
			offset += int64(n)
		}
	}
	return nil
}

// readBlock reads a full filesystem block into buf.
func (fs *FileSystem) readBlock(blockNum uint32, buf []byte) error {
	return fs.readBytes(int64(blockNum)*int64(fs.blockSize), buf[:fs.blockSize])
}

// writeBytes writes len(buf) bytes to the block device at the given byte offset.
func (fs *FileSystem) writeBytes(offset int64, buf []byte) error {
	devBlockSize := int64(fs.device.BlockSize())
	for len(buf) > 0 {
		lba := uint64(offset / devBlockSize)
		off := int(offset % devBlockSize)

		if off == 0 && len(buf) >= int(devBlockSize) {
			if err := fs.device.WriteBlock(lba, buf[:devBlockSize]); err != nil {
				return err
			}
			buf = buf[devBlockSize:]
			offset += devBlockSize
		} else {
			// Read-modify-write for partial sector
			if err := fs.device.ReadBlock(lba, fs.sectorBuf[:]); err != nil {
				return err
			}
			n := int(devBlockSize) - off
			if n > len(buf) {
				n = len(buf)
			}
			copy(fs.sectorBuf[off:off+n], buf[:n])
			if err := fs.device.WriteBlock(lba, fs.sectorBuf[:]); err != nil {
				return err
			}
			buf = buf[n:]
			offset += int64(n)
		}
	}
	return nil
}

// writeBlock writes a full filesystem block to disk.
func (fs *FileSystem) writeBlock(blockNum uint32, buf []byte) error {
	return fs.writeBytes(int64(blockNum)*int64(fs.blockSize), buf[:fs.blockSize])
}

// ReadInode reads an inode by its 1-based inode number.
func (fs *FileSystem) ReadInode(inum uint32) (*Inode, error) {
	if inum == 0 || inum > fs.sb.InodesCount {
		return nil, ErrInvalidInode
	}
	g := (inum - 1) / fs.sb.InodesPerGroup
	idx := (inum - 1) % fs.sb.InodesPerGroup
	offset := int64(fs.groups[g].InodeTable)*int64(fs.blockSize) + int64(idx)*int64(fs.sb.InodeSize)

	var inBuf [InodeSize128]byte
	if err := fs.readBytes(offset, inBuf[:]); err != nil {
		return nil, ErrReadFailed
	}
	return UnmarshalInode(inBuf[:])
}

// ReadDir reads all directory entries from a directory inode.
func (fs *FileSystem) ReadDir(inum uint32) ([]DirEntry, error) {
	inode, err := fs.ReadInode(inum)
	if err != nil {
		return nil, err
	}
	if !inode.IsDir() {
		return nil, ErrNotDir
	}

	var entries []DirEntry
	buf := make([]byte, fs.blockSize)
	blocksUsed := inode.Size / fs.blockSize

	for i := uint32(0); i < blocksUsed; i++ {
		blockNum, err := fs.inodeBlockNum(inode, i)
		if err != nil {
			return nil, err
		}
		if blockNum == 0 {
			break
		}
		if err := fs.readBlock(blockNum, buf); err != nil {
			return nil, err
		}

		offset := 0
		for offset < int(fs.blockSize) {
			de, consumed, err := UnmarshalDirEntry(buf[offset:])
			if err != nil {
				return nil, err
			}
			if consumed == 0 {
				break
			}
			if de.Inode != 0 {
				entries = append(entries, *de)
			}
			offset += consumed
		}
	}

	return entries, nil
}

// LookupDir finds a directory entry by name within a directory inode.
func (fs *FileSystem) LookupDir(inum uint32, name string) (*DirEntry, error) {
	entries, err := fs.ReadDir(inum)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i], nil
		}
	}
	return nil, ErrNotFound
}

// inodeBlockNum resolves the nth data block for an inode, handling
// direct, single-indirect, and double-indirect block pointers.
func (fs *FileSystem) inodeBlockNum(inode *Inode, n uint32) (uint32, error) {
	ptrsPerBlock := fs.blockSize / 4

	// Direct blocks (0-11)
	if n < NDirect {
		return inode.Block[n], nil
	}
	n -= NDirect

	// Single indirect
	if n < ptrsPerBlock {
		if inode.Block[IndirectBlock] == 0 {
			return 0, nil
		}
		return fs.readBlockPtr(inode.Block[IndirectBlock], n)
	}
	n -= ptrsPerBlock

	// Double indirect
	if n < ptrsPerBlock*ptrsPerBlock {
		if inode.Block[DblIndirectBlock] == 0 {
			return 0, nil
		}
		idx1 := n / ptrsPerBlock
		idx2 := n % ptrsPerBlock
		indBlock, err := fs.readBlockPtr(inode.Block[DblIndirectBlock], idx1)
		if err != nil {
			return 0, err
		}
		if indBlock == 0 {
			return 0, nil
		}
		return fs.readBlockPtr(indBlock, idx2)
	}

	return 0, ErrCorrupted
}

// ResolveBlockList returns the physical block numbers for data blocks
// startBlock..startBlock+count-1 of the given inode. Caches indirect
// pointer tables internally so that a contiguous range of blocks reads
// each metadata block at most once (vs. once per pointer via inodeBlockNum).
func (fs *FileSystem) ResolveBlockList(inode *Inode, startBlock, count uint32) ([]uint32, error) {
	ptrsPerBlock := fs.blockSize / 4
	blocks := make([]uint32, 0, count)

	const noBlock = ^uint32(0)
	// Two-level cache for indirect pointer tables.
	// cache1: single-indirect table or double-indirect L1 table
	// cache2: double-indirect L2 table
	cache1Num, cache2Num := noBlock, noBlock
	var cache1, cache2 []byte

	readCachedPtr := func(cache *[]byte, cacheNum *uint32, blockNum, idx uint32) (uint32, error) {
		if *cacheNum != blockNum {
			if *cache == nil {
				*cache = make([]byte, fs.blockSize)
			}
			if err := fs.readBlock(blockNum, *cache); err != nil {
				return 0, err
			}
			*cacheNum = blockNum
		}
		return binary.LittleEndian.Uint32((*cache)[idx*4:]), nil
	}

	for i := uint32(0); i < count; i++ {
		n := startBlock + i

		// Direct blocks (0-11)
		if n < NDirect {
			blocks = append(blocks, inode.Block[n])
			continue
		}
		adj := n - NDirect

		// Single indirect
		if adj < ptrsPerBlock {
			if inode.Block[IndirectBlock] == 0 {
				blocks = append(blocks, 0)
				continue
			}
			bn, err := readCachedPtr(&cache1, &cache1Num, inode.Block[IndirectBlock], adj)
			if err != nil {
				return blocks, err
			}
			blocks = append(blocks, bn)
			continue
		}
		adj -= ptrsPerBlock

		// Double indirect
		if adj < ptrsPerBlock*ptrsPerBlock {
			if inode.Block[DblIndirectBlock] == 0 {
				blocks = append(blocks, 0)
				continue
			}
			idx1 := adj / ptrsPerBlock
			idx2 := adj % ptrsPerBlock
			indBlock, err := readCachedPtr(&cache1, &cache1Num, inode.Block[DblIndirectBlock], idx1)
			if err != nil {
				return blocks, err
			}
			if indBlock == 0 {
				blocks = append(blocks, 0)
				continue
			}
			bn, err := readCachedPtr(&cache2, &cache2Num, indBlock, idx2)
			if err != nil {
				return blocks, err
			}
			blocks = append(blocks, bn)
			continue
		}

		return blocks, ErrCorrupted
	}

	return blocks, nil
}

// readBlockPtr reads a uint32 block pointer at index idx from a block of pointers.
func (fs *FileSystem) readBlockPtr(blockNum uint32, idx uint32) (uint32, error) {
	offset := int64(blockNum)*int64(fs.blockSize) + int64(idx)*4
	var buf [4]byte
	if err := fs.readBytes(offset, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// OpenInum opens a file by its inode number. The returned File has its
// position set to 0. Unlike Open, this works for both files and directories
// (the caller is responsible for type-checking).
func (fs *FileSystem) OpenInum(inum uint32) (*File, error) {
	inode, err := fs.ReadInode(inum)
	if err != nil {
		return nil, err
	}
	return &File{
		fs:       fs,
		inum:     inum,
		inode:    *inode,
		blockIdx: -1,
	}, nil
}

// Open opens a file by path (e.g., "/fonts/sans.ttf").
func (fs *FileSystem) Open(path string) (*File, error) {
	if len(path) == 0 || path[0] != '/' {
		return nil, ErrInvalidPath
	}

	components := splitPath(path)
	if len(components) == 0 {
		return nil, ErrInvalidPath
	}

	currentInum := uint32(InodeRoot)
	for i, component := range components {
		de, err := fs.LookupDir(currentInum, component)
		if err != nil {
			return nil, err
		}

		isLast := i == len(components)-1
		if isLast {
			inode, err := fs.ReadInode(de.Inode)
			if err != nil {
				return nil, err
			}
			if inode.IsDir() {
				return nil, ErrNotFile
			}
			return &File{
				fs:    fs,
				inum:  de.Inode,
				inode: *inode,
				name:  de.Name,
			}, nil
		}

		// Not last — must be a directory
		if de.FileType != FTDir {
			return nil, ErrNotFound
		}
		currentInum = de.Inode
	}

	return nil, ErrNotFound
}

// splitPath splits a path like "/a/b/c" into ["a", "b", "c"].
func splitPath(path string) []string {
	var components []string
	start := -1
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if start >= 0 {
				components = append(components, path[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		components = append(components, path[start:])
	}
	return components
}

// Superblock returns the filesystem's superblock.
func (fs *FileSystem) Superblock() *Superblock {
	return &fs.sb
}

// BlockSize returns the filesystem block size in bytes.
func (fs *FileSystem) BlockSizeBytes() uint32 {
	return fs.blockSize
}
