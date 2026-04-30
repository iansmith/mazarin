package ext2

import (
	"encoding/binary"
	"fmt"
)

// DirVerifyHook, if non-nil, is invoked after addDirEntry/removeDirEntry
// detects a corrupt parent directory. fs.maz wires up a Printf-based hook
// during diagnostic runs to identify in-flight dirent corruption.
//
// Receives the parent inode number, the operation that just completed
// (e.g. "add:foo.zap" / "remove:foo.zap"), and the first invariant
// violation found in the parent's dir blocks.
var DirVerifyHook func(parentInum uint32, op string, err error)

// ErrReadOnly is returned when a write operation is attempted on a read-only mount.
var ErrReadOnly = &Ext2Error{"filesystem mounted read-only"}

// allocBlock finds and allocates a free block, returning its absolute block number.
func (fs *FileSystem) allocBlock() (uint32, error) {
	if !fs.writable {
		return 0, ErrReadOnly
	}
	for g := uint32(0); g < fs.numGroups; g++ {
		if fs.groups[g].FreeBlocksCount == 0 {
			continue
		}
		bm := fs.blockBitmaps[g]
		groupStart := fs.sb.FirstDataBlock + g*fs.sb.BlocksPerGroup
		for byteIdx := 0; byteIdx < len(bm); byteIdx++ {
			if bm[byteIdx] == 0xFF {
				continue
			}
			for bit := uint32(0); bit < 8; bit++ {
				if bm[byteIdx]&(1<<bit) == 0 {
					bm[byteIdx] |= 1 << bit
					fs.groups[g].FreeBlocksCount--
					fs.sb.FreeBlocksCount--
					return groupStart + uint32(byteIdx)*8 + bit, nil
				}
			}
		}
	}
	return 0, ErrNoSpace
}

// freeBlock marks a block as free in the bitmap.
func (fs *FileSystem) freeBlock(blockNum uint32) error {
	if !fs.writable {
		return ErrReadOnly
	}
	g := (blockNum - fs.sb.FirstDataBlock) / fs.sb.BlocksPerGroup
	local := (blockNum - fs.sb.FirstDataBlock) % fs.sb.BlocksPerGroup
	byteIdx := local / 8
	bit := local % 8

	if fs.blockBitmaps[g][byteIdx]&(1<<bit) == 0 {
		return ErrCorrupted // double-free
	}
	fs.blockBitmaps[g][byteIdx] &^= 1 << bit
	fs.groups[g].FreeBlocksCount++
	fs.sb.FreeBlocksCount++
	return nil
}

// allocInode finds and allocates a free inode, returning its 1-based inode number.
func (fs *FileSystem) allocInode() (uint32, error) {
	if !fs.writable {
		return 0, ErrReadOnly
	}
	for g := uint32(0); g < fs.numGroups; g++ {
		if fs.groups[g].FreeInodesCount == 0 {
			continue
		}
		bm := fs.inodeBitmaps[g]
		for byteIdx := uint32(0); byteIdx < fs.sb.InodesPerGroup/8; byteIdx++ {
			if bm[byteIdx] == 0xFF {
				continue
			}
			for bit := uint32(0); bit < 8; bit++ {
				if bm[byteIdx]&(1<<bit) == 0 {
					bm[byteIdx] |= 1 << bit
					fs.groups[g].FreeInodesCount--
					fs.sb.FreeInodesCount--
					return g*fs.sb.InodesPerGroup + byteIdx*8 + bit + 1, nil
				}
			}
		}
	}
	return 0, ErrNoInodes
}

// freeInode marks an inode as free in the bitmap.
func (fs *FileSystem) freeInode(inum uint32) error {
	if !fs.writable {
		return ErrReadOnly
	}
	g := (inum - 1) / fs.sb.InodesPerGroup
	idx := (inum - 1) % fs.sb.InodesPerGroup
	byteIdx := idx / 8
	bit := idx % 8

	if fs.inodeBitmaps[g][byteIdx]&(1<<bit) == 0 {
		return ErrCorrupted // double-free
	}
	fs.inodeBitmaps[g][byteIdx] &^= 1 << bit
	fs.groups[g].FreeInodesCount++
	fs.sb.FreeInodesCount++
	return nil
}

// WriteInode writes an inode to disk.
func (fs *FileSystem) WriteInode(inum uint32, inode *Inode) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.writeInodeLocked(inum, inode)
}

// writeInodeLocked is the implementation of WriteInode. Caller must hold
// fs.mu (Lock).
func (fs *FileSystem) writeInodeLocked(inum uint32, inode *Inode) error {
	if inum == 0 || inum > fs.sb.InodesCount {
		return ErrInvalidInode
	}
	g := (inum - 1) / fs.sb.InodesPerGroup
	idx := (inum - 1) % fs.sb.InodesPerGroup
	offset := int64(fs.groups[g].InodeTable)*int64(fs.blockSize) + int64(idx)*int64(fs.sb.InodeSize)
	buf := inode.Marshal()
	return fs.writeBytes(offset, buf[:])
}

// setInodeBlock sets the nth data block pointer in an inode, allocating
// indirect blocks as needed.
func (fs *FileSystem) setInodeBlock(inode *Inode, n uint32, blockNum uint32) error {
	ptrsPerBlock := fs.blockSize / 4

	// Direct blocks
	if n < NDirect {
		inode.Block[n] = blockNum
		return nil
	}
	n -= NDirect

	// Single indirect
	if n < ptrsPerBlock {
		if inode.Block[IndirectBlock] == 0 {
			indBlock, err := fs.allocBlock()
			if err != nil {
				return err
			}
			if err := fs.writeBlock(indBlock, make([]byte, fs.blockSize)); err != nil {
				return err
			}
			inode.Block[IndirectBlock] = indBlock
			inode.Blocks += fs.blockSize / 512
		}
		return fs.writeBlockPtr(inode.Block[IndirectBlock], n, blockNum)
	}
	n -= ptrsPerBlock

	// Double indirect
	if n < ptrsPerBlock*ptrsPerBlock {
		if inode.Block[DblIndirectBlock] == 0 {
			dblBlock, err := fs.allocBlock()
			if err != nil {
				return err
			}
			if err := fs.writeBlock(dblBlock, make([]byte, fs.blockSize)); err != nil {
				return err
			}
			inode.Block[DblIndirectBlock] = dblBlock
			inode.Blocks += fs.blockSize / 512
		}
		idx1 := n / ptrsPerBlock
		idx2 := n % ptrsPerBlock

		indBlock, err := fs.readBlockPtr(inode.Block[DblIndirectBlock], idx1)
		if err != nil {
			return err
		}
		if indBlock == 0 {
			indBlock, err = fs.allocBlock()
			if err != nil {
				return err
			}
			if err := fs.writeBlock(indBlock, make([]byte, fs.blockSize)); err != nil {
				return err
			}
			if err := fs.writeBlockPtr(inode.Block[DblIndirectBlock], idx1, indBlock); err != nil {
				return err
			}
			inode.Blocks += fs.blockSize / 512
		}
		return fs.writeBlockPtr(indBlock, idx2, blockNum)
	}

	return ErrNoSpace
}

// writeBlockPtr writes a uint32 block pointer at index idx in a block of pointers.
func (fs *FileSystem) writeBlockPtr(blockNum uint32, idx uint32, val uint32) error {
	offset := int64(blockNum)*int64(fs.blockSize) + int64(idx)*4
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], val)
	return fs.writeBytes(offset, buf[:])
}

// freeInodeBlocks releases all data blocks owned by an inode.
func (fs *FileSystem) freeInodeBlocks(inode *Inode) error {
	ptrsPerBlock := fs.blockSize / 4
	totalDataBlocks := inode.Size / fs.blockSize
	if inode.Size%fs.blockSize != 0 {
		totalDataBlocks++
	}

	for i := uint32(0); i < totalDataBlocks; i++ {
		blockNum, err := fs.inodeBlockNum(inode, i)
		if err != nil {
			return err
		}
		if blockNum != 0 {
			if err := fs.freeBlock(blockNum); err != nil {
				return err
			}
		}
	}

	// Free indirect blocks themselves
	if inode.Block[IndirectBlock] != 0 {
		if err := fs.freeBlock(inode.Block[IndirectBlock]); err != nil {
			return err
		}
	}
	if inode.Block[DblIndirectBlock] != 0 {
		// Free second-level indirect blocks
		for i := uint32(0); i < ptrsPerBlock; i++ {
			indBlock, err := fs.readBlockPtr(inode.Block[DblIndirectBlock], i)
			if err != nil {
				return err
			}
			if indBlock != 0 {
				if err := fs.freeBlock(indBlock); err != nil {
					return err
				}
			}
		}
		if err := fs.freeBlock(inode.Block[DblIndirectBlock]); err != nil {
			return err
		}
	}

	return nil
}

// addDirEntry adds a directory entry to the directory owned by parentInum.
func (fs *FileSystem) addDirEntry(parentInum uint32, name string, childInum uint32, fileType uint8) error {
	err := fs.addDirEntryImpl(parentInum, name, childInum, fileType)
	if DirVerifyHook != nil {
		if vErr := fs.verifyDirInvariants(parentInum); vErr != nil {
			DirVerifyHook(parentInum, "add:"+name, vErr)
		}
	}
	return err
}

func (fs *FileSystem) addDirEntryImpl(parentInum uint32, name string, childInum uint32, fileType uint8) error {
	parent, err := fs.readInodeLocked(parentInum)
	if err != nil {
		return err
	}

	needed := DirEntryRealSize(len(name))
	buf := make([]byte, fs.blockSize)

	// Walk directory blocks looking for space in the last entry's slack
	blocksUsed := parent.Size / fs.blockSize
	for i := uint32(0); i < blocksUsed; i++ {
		blockNum, err := fs.inodeBlockNum(parent, i)
		if err != nil {
			return err
		}
		if err := fs.readBlock(blockNum, buf); err != nil {
			return err
		}

		offset := uint16(0)
		for offset < uint16(fs.blockSize) {
			de, _, err := UnmarshalDirEntry(buf[offset:])
			if err != nil {
				return err
			}
			actualSize := DirEntryRealSize(int(de.NameLen))
			slack := de.RecLen - actualSize

			if slack >= needed {
				oldRecLen := de.RecLen
				de.RecLen = actualSize
				MarshalDirEntry(buf[offset:], de)

				newEntry := NewDirEntry(childInum, name, fileType)
				newEntry.RecLen = oldRecLen - actualSize
				MarshalDirEntry(buf[offset+actualSize:], newEntry)

				return fs.writeBlock(blockNum, buf)
			}

			offset += de.RecLen
		}
	}

	// No space — allocate a new directory block
	newBlock, err := fs.allocBlock()
	if err != nil {
		return err
	}

	dirData := make([]byte, fs.blockSize)
	newEntry := NewDirEntry(childInum, name, fileType)
	newEntry.RecLen = uint16(fs.blockSize)
	MarshalDirEntry(dirData, newEntry)

	if err := fs.writeBlock(newBlock, dirData); err != nil {
		return err
	}

	blockIdx := parent.Size / fs.blockSize
	if err := fs.setInodeBlock(parent, blockIdx, newBlock); err != nil {
		return err
	}
	parent.Size += fs.blockSize
	parent.Blocks += fs.blockSize / 512
	return fs.writeInodeLocked(parentInum, parent)
}

// removeDirEntry removes a directory entry by name from the directory.
// It merges the removed entry's space into the previous entry's RecLen.
func (fs *FileSystem) removeDirEntry(parentInum uint32, name string) error {
	err := fs.removeDirEntryImpl(parentInum, name)
	if DirVerifyHook != nil {
		if vErr := fs.verifyDirInvariants(parentInum); vErr != nil {
			DirVerifyHook(parentInum, "remove:"+name, vErr)
		}
	}
	return err
}

func (fs *FileSystem) removeDirEntryImpl(parentInum uint32, name string) error {
	parent, err := fs.readInodeLocked(parentInum)
	if err != nil {
		return err
	}

	buf := make([]byte, fs.blockSize)
	blocksUsed := parent.Size / fs.blockSize

	for i := uint32(0); i < blocksUsed; i++ {
		blockNum, err := fs.inodeBlockNum(parent, i)
		if err != nil {
			return err
		}
		if err := fs.readBlock(blockNum, buf); err != nil {
			return err
		}

		prevOffset := uint16(0)
		offset := uint16(0)
		isFirst := true
		for offset < uint16(fs.blockSize) {
			de, _, err := UnmarshalDirEntry(buf[offset:])
			if err != nil {
				return err
			}
			if de.RecLen == 0 {
				break
			}

			if de.Name == name && de.Inode != 0 {
				if isFirst {
					// First entry in block — zero the inode to mark deleted
					de.Inode = 0
					MarshalDirEntry(buf[offset:], de)
				} else {
					// Merge into previous entry
					prev, _, err := UnmarshalDirEntry(buf[prevOffset:])
					if err != nil {
						return err
					}
					prev.RecLen += de.RecLen
					MarshalDirEntry(buf[prevOffset:], prev)
				}
				return fs.writeBlock(blockNum, buf)
			}

			prevOffset = offset
			offset += de.RecLen
			isFirst = false
		}
	}

	return ErrNotFound
}

// Create creates a new regular file in the filesystem and returns the open file.
// The parent directory must exist.
func (fs *FileSystem) Create(path string, mode uint16) (*File, error) {
	if !fs.writable {
		return nil, ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	parentInum, name, err := fs.resolvePath(path)
	if err != nil {
		return nil, err
	}

	// Check it doesn't already exist
	if _, err := fs.lookupDirLocked(parentInum, name); err == nil {
		return nil, ErrExists
	}

	inum, err := fs.allocInode()
	if err != nil {
		return nil, err
	}

	inode := &Inode{
		Mode:       TypeFile | (mode & 0x0FFF),
		LinksCount: 1,
	}

	if err := fs.writeInodeLocked(inum, inode); err != nil {
		return nil, err
	}
	if err := fs.addDirEntry(parentInum, name, inum, FTFile); err != nil {
		return nil, err
	}

	return &File{
		fs:       fs,
		inum:     inum,
		inode:    *inode,
		name:     name,
		blockIdx: -1,
	}, nil
}

// WriteFile creates (or overwrites) a file at path with the given data and permissions.
func (fs *FileSystem) WriteFile(path string, data []byte, mode uint16) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	parentInum, name, err := fs.resolvePath(path)
	if err != nil {
		return err
	}

	// Check if file exists — if so, remove and recreate. Dirent first so
	// path lookups see ENOENT immediately; defer the bitmap/inode/blocks
	// free if any open handle still pins the inum (Linux unlink-while-open).
	if de, err := fs.lookupDirLocked(parentInum, name); err == nil {
		inode, err := fs.readInodeLocked(de.Inode)
		if err != nil {
			return err
		}
		if inode.IsDir() {
			return ErrNotFile
		}
		if err := fs.removeDirEntry(parentInum, name); err != nil {
			return err
		}
		fs.pinMu.Lock()
		pinned := fs.isInodePinnedLocked(de.Inode)
		if pinned {
			fs.markPendingFreeLocked(de.Inode)
		}
		fs.pinMu.Unlock()
		if !pinned {
			if err := fs.freeInodeBlocks(inode); err != nil {
				return err
			}
			var zeroInode Inode
			if err := fs.writeInodeLocked(de.Inode, &zeroInode); err != nil {
				return err
			}
			if err := fs.freeInode(de.Inode); err != nil {
				return err
			}
		}
	}

	inum, err := fs.allocInode()
	if err != nil {
		return err
	}

	inode := &Inode{
		Mode:       TypeFile | (mode & 0x0FFF),
		Size:       uint32(len(data)),
		LinksCount: 1,
	}

	// Write data blocks
	fullBlocks := uint32(len(data)) / fs.blockSize
	remaining := uint32(len(data)) % fs.blockSize
	totalDataBlocks := fullBlocks
	if remaining > 0 {
		totalDataBlocks++
	}

	for i := uint32(0); i < totalDataBlocks; i++ {
		block, err := fs.allocBlock()
		if err != nil {
			return err
		}
		if err := fs.setInodeBlock(inode, i, block); err != nil {
			return err
		}

		start := i * fs.blockSize
		end := start + fs.blockSize
		if end > uint32(len(data)) {
			padded := make([]byte, fs.blockSize)
			copy(padded, data[start:])
			if err := fs.writeBlock(block, padded); err != nil {
				return err
			}
		} else {
			if err := fs.writeBlock(block, data[start:end]); err != nil {
				return err
			}
		}
		inode.Blocks += fs.blockSize / 512
	}

	if err := fs.writeInodeLocked(inum, inode); err != nil {
		return err
	}

	return fs.addDirEntry(parentInum, name, inum, FTFile)
}

// Mkdir creates a directory at the given path.
func (fs *FileSystem) Mkdir(path string, mode uint16) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	parentInum, name, err := fs.resolvePath(path)
	if err != nil {
		return err
	}

	if _, err := fs.lookupDirLocked(parentInum, name); err == nil {
		return ErrExists
	}

	inum, err := fs.allocInode()
	if err != nil {
		return err
	}

	dataBlock, err := fs.allocBlock()
	if err != nil {
		return err
	}

	inode := &Inode{
		Mode:       TypeDir | (mode & 0x0FFF),
		Size:       fs.blockSize,
		LinksCount: 2, // . and parent's entry
		Blocks:     fs.blockSize / 512,
	}
	inode.Block[0] = dataBlock

	// Write . and .. entries
	dirData := make([]byte, fs.blockSize)
	dot := NewDirEntry(inum, ".", FTDir)
	n := MarshalDirEntry(dirData, dot)
	dotdot := NewDirEntry(parentInum, "..", FTDir)
	dotdot.RecLen = uint16(fs.blockSize) - uint16(n)
	MarshalDirEntry(dirData[n:], dotdot)

	if err := fs.writeBlock(dataBlock, dirData); err != nil {
		return err
	}
	if err := fs.writeInodeLocked(inum, inode); err != nil {
		return err
	}
	if err := fs.addDirEntry(parentInum, name, inum, FTDir); err != nil {
		return err
	}

	// Increment parent's link count (for ..)
	parent, err := fs.readInodeLocked(parentInum)
	if err != nil {
		return err
	}
	parent.LinksCount++
	if err := fs.writeInodeLocked(parentInum, parent); err != nil {
		return err
	}

	// Update group's UsedDirsCount
	g := (inum - 1) / fs.sb.InodesPerGroup
	fs.groups[g].UsedDirsCount++

	return nil
}

// Remove removes a file or empty directory at the given path.
func (fs *FileSystem) Remove(path string) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	parentInum, name, err := fs.resolvePath(path)
	if err != nil {
		return err
	}

	de, err := fs.lookupDirLocked(parentInum, name)
	if err != nil {
		return err
	}

	inode, err := fs.readInodeLocked(de.Inode)
	if err != nil {
		return err
	}

	if inode.IsDir() {
		// Check directory is empty (only . and ..)
		entries, err := fs.readDirLocked(de.Inode)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name != "." && e.Name != ".." {
				return ErrNotEmpty
			}
		}

		// Decrement parent's link count
		parent, err := fs.readInodeLocked(parentInum)
		if err != nil {
			return err
		}
		parent.LinksCount--
		if err := fs.writeInodeLocked(parentInum, parent); err != nil {
			return err
		}

		// Update group's UsedDirsCount
		g := (de.Inode - 1) / fs.sb.InodesPerGroup
		fs.groups[g].UsedDirsCount--
	}

	// Remove the directory entry first so subsequent path lookups return
	// ENOENT immediately (Linux unlink semantics). Then either reclaim
	// inode/blocks/bitmap now, or defer the reclaim if an open handle
	// still pins the inum.
	if err := fs.removeDirEntry(parentInum, name); err != nil {
		return err
	}

	fs.pinMu.Lock()
	pinned := fs.isInodePinnedLocked(de.Inode)
	if pinned {
		fs.markPendingFreeLocked(de.Inode)
	}
	fs.pinMu.Unlock()
	if pinned {
		return nil
	}

	// Free data blocks
	if err := fs.freeInodeBlocks(inode); err != nil {
		return err
	}

	// Zero out the inode on disk so e2fsck doesn't see stale data
	var zeroInode Inode
	if err := fs.writeInodeLocked(de.Inode, &zeroInode); err != nil {
		return err
	}

	// Free inode in bitmap
	return fs.freeInode(de.Inode)
}

// Rename moves/renames a file or directory from oldPath to newPath.
func (fs *FileSystem) Rename(oldPath, newPath string) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	oldParent, oldName, err := fs.resolvePath(oldPath)
	if err != nil {
		return err
	}
	newParent, newName, err := fs.resolvePath(newPath)
	if err != nil {
		return err
	}

	// Find source entry
	de, err := fs.lookupDirLocked(oldParent, oldName)
	if err != nil {
		return err
	}

	// If target exists, remove it first. Dirent goes first; defer
	// bitmap/blocks reclaim if the inum is pinned by an open handle.
	if existDe, err := fs.lookupDirLocked(newParent, newName); err == nil {
		existInode, err := fs.readInodeLocked(existDe.Inode)
		if err != nil {
			return err
		}
		if existInode.IsDir() {
			// Can't overwrite a directory
			return ErrExists
		}
		if err := fs.removeDirEntry(newParent, newName); err != nil {
			return err
		}
		fs.pinMu.Lock()
		pinned := fs.isInodePinnedLocked(existDe.Inode)
		if pinned {
			fs.markPendingFreeLocked(existDe.Inode)
		}
		fs.pinMu.Unlock()
		if !pinned {
			if err := fs.freeInodeBlocks(existInode); err != nil {
				return err
			}
			if err := fs.freeInode(existDe.Inode); err != nil {
				return err
			}
		}
	}

	// Add new entry pointing to same inode
	if err := fs.addDirEntry(newParent, newName, de.Inode, de.FileType); err != nil {
		return err
	}

	// Remove old entry
	if err := fs.removeDirEntry(oldParent, oldName); err != nil {
		return err
	}

	// If moving a directory, update its .. entry
	if de.FileType == FTDir && oldParent != newParent {
		inode, err := fs.readInodeLocked(de.Inode)
		if err != nil {
			return err
		}

		// Read first data block (contains . and ..)
		buf := make([]byte, fs.blockSize)
		if err := fs.readBlock(inode.Block[0], buf); err != nil {
			return err
		}

		// Skip . entry, update .. entry's inode
		dotDe, consumed, err := UnmarshalDirEntry(buf)
		if err != nil {
			return err
		}
		_ = dotDe
		dotdotDe, _, err := UnmarshalDirEntry(buf[consumed:])
		if err != nil {
			return err
		}
		dotdotDe.Inode = newParent
		MarshalDirEntry(buf[consumed:], dotdotDe)
		if err := fs.writeBlock(inode.Block[0], buf); err != nil {
			return err
		}

		// Update link counts
		oldP, _ := fs.readInodeLocked(oldParent)
		oldP.LinksCount--
		fs.writeInodeLocked(oldParent, oldP)

		newP, _ := fs.readInodeLocked(newParent)
		newP.LinksCount++
		fs.writeInodeLocked(newParent, newP)
	}

	return nil
}

// SetTimes updates the access and modification times on a file or directory.
func (fs *FileSystem) SetTimes(path string, atime, mtime uint32) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	inum, err := fs.resolveInodeLocked(path)
	if err != nil {
		return err
	}

	inode, err := fs.readInodeLocked(inum)
	if err != nil {
		return err
	}

	inode.ATime = atime
	inode.MTime = mtime
	return fs.writeInodeLocked(inum, inode)
}

// SetMode updates the permission bits on a file or directory.
func (fs *FileSystem) SetMode(path string, mode uint16) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	inum, err := fs.resolveInodeLocked(path)
	if err != nil {
		return err
	}

	inode, err := fs.readInodeLocked(inum)
	if err != nil {
		return err
	}

	inode.Mode = (inode.Mode & TypeMask) | (mode & 0x0FFF)
	return fs.writeInodeLocked(inum, inode)
}

// Truncate sets the size of the inode to newSize.
// Shrinking zeroes the tail of the last block but does not free blocks.
// Extending creates a sparse hole (reads return zero).
func (fs *FileSystem) Truncate(inum uint32, newSize uint32) error {
	if !fs.writable {
		return ErrReadOnly
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	inode, err := fs.readInodeLocked(inum)
	if err != nil {
		return err
	}
	inode.Size = newSize
	return fs.writeInodeLocked(inum, inode)
}

// Sync flushes all in-memory metadata (bitmaps, group descriptors, superblock) to disk.
func (fs *FileSystem) Sync() error {
	if !fs.writable {
		return nil // nothing to sync on read-only mount
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Write bitmaps
	for g := uint32(0); g < fs.numGroups; g++ {
		if err := fs.writeBlock(fs.groups[g].BlockBitmap, fs.blockBitmaps[g]); err != nil {
			return err
		}
		if err := fs.writeBlock(fs.groups[g].InodeBitmap, fs.inodeBitmaps[g]); err != nil {
			return err
		}
	}

	// Write group descriptor table (block after superblock)
	gdtBlock := fs.sb.FirstDataBlock + 1
	gdtBuf := make([]byte, fs.numGroups*GroupDescSize)
	for i := uint32(0); i < fs.numGroups; i++ {
		gdData := fs.groups[i].Marshal()
		copy(gdtBuf[i*GroupDescSize:], gdData[:])
	}
	if err := fs.writeBytes(int64(gdtBlock)*int64(fs.blockSize), gdtBuf); err != nil {
		return err
	}

	// Write superblock
	sbData := fs.sb.Marshal()
	return fs.writeBytes(SuperblockOffset, sbData[:])
}

// resolvePath splits "/a/b/name" into (parent_inode_of_/a/b, "name", nil).
// Caller must hold fs.mu (RLock or Lock).
func (fs *FileSystem) resolvePath(path string) (uint32, string, error) {
	if len(path) == 0 || path[0] != '/' {
		return 0, "", ErrInvalidPath
	}
	components := splitPath(path)
	if len(components) == 0 {
		return 0, "", ErrInvalidPath
	}

	name := components[len(components)-1]
	parentInum := uint32(InodeRoot)

	for _, component := range components[:len(components)-1] {
		de, err := fs.lookupDirLocked(parentInum, component)
		if err != nil {
			return 0, "", err
		}
		if de.FileType != FTDir {
			return 0, "", ErrNotDir
		}
		parentInum = de.Inode
	}

	return parentInum, name, nil
}

// ResolveInode resolves a path to its inode number.
func (fs *FileSystem) ResolveInode(path string) (uint32, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.resolveInodeLocked(path)
}

// resolveInodeLocked is the implementation of ResolveInode. Caller must
// hold fs.mu (RLock or Lock).
func (fs *FileSystem) resolveInodeLocked(path string) (uint32, error) {
	if path == "/" {
		return InodeRoot, nil
	}
	parentInum, name, err := fs.resolvePath(path)
	if err != nil {
		return 0, err
	}
	de, err := fs.lookupDirLocked(parentInum, name)
	if err != nil {
		return 0, err
	}
	return de.Inode, nil
}

// verifyDirInvariants walks every dir block belonging to parentInum and
// checks the on-disk dirent layout against ext2 invariants:
//
//   - every entry's RecLen >= DirEntryRealSize(NameLen)
//   - every entry's RecLen fits within the remaining block bytes
//   - the sum of RecLens in a block equals exactly fs.blockSize
//     (the last entry's RecLen extends to fill the block)
//   - among entries with Inode != 0, names are unique
//   - each Inode != 0 references an in-range inode whose on-disk type
//     matches the entry's FileType field
//
// Returns the first invariant violation found, with enough context to
// identify the bad block / offset. Designed for diagnostic use only —
// gated behind DirVerifyHook so it's a no-op when the hook is unset.
func (fs *FileSystem) verifyDirInvariants(parentInum uint32) error {
	parent, err := fs.readInodeLocked(parentInum)
	if err != nil {
		return fmt.Errorf("ReadInode parent=%d: %w", parentInum, err)
	}
	if !parent.IsDir() {
		return nil
	}

	blocksUsed := parent.Size / fs.blockSize
	buf := make([]byte, fs.blockSize)
	seen := make(map[string]struct{})

	for i := uint32(0); i < blocksUsed; i++ {
		blockNum, err := fs.inodeBlockNum(parent, i)
		if err != nil {
			return fmt.Errorf("inodeBlockNum block=%d: %w", i, err)
		}
		if blockNum == 0 {
			continue // sparse — not expected in a dir, but skip if so
		}
		if err := fs.readBlock(blockNum, buf); err != nil {
			return fmt.Errorf("readBlock block=%d phys=%d: %w", i, blockNum, err)
		}

		offset := uint16(0)
		for offset < uint16(fs.blockSize) {
			if int(offset)+DirEntryHeaderSize > int(fs.blockSize) {
				return fmt.Errorf("block=%d offset=%d: header would overrun block", i, offset)
			}
			de, _, derr := UnmarshalDirEntry(buf[offset:])
			if derr != nil {
				return fmt.Errorf("block=%d offset=%d: unmarshal: %w", i, offset, derr)
			}
			if de.RecLen == 0 {
				return fmt.Errorf("block=%d offset=%d: zero RecLen", i, offset)
			}
			minSize := DirEntryRealSize(int(de.NameLen))
			if de.RecLen < minSize {
				return fmt.Errorf("block=%d offset=%d: RecLen=%d < min=%d (NameLen=%d)",
					i, offset, de.RecLen, minSize, de.NameLen)
			}
			if int(offset)+int(de.RecLen) > int(fs.blockSize) {
				return fmt.Errorf("block=%d offset=%d: RecLen=%d overruns block end",
					i, offset, de.RecLen)
			}
			if de.Inode != 0 {
				if _, dup := seen[de.Name]; dup {
					return fmt.Errorf("block=%d offset=%d: duplicate name %q in dir %d",
						i, offset, de.Name, parentInum)
				}
				seen[de.Name] = struct{}{}
				if de.Inode > fs.sb.InodesCount {
					return fmt.Errorf("block=%d offset=%d: name=%q inode=%d > total=%d",
						i, offset, de.Name, de.Inode, fs.sb.InodesCount)
				}
				if de.FileType != 0 {
					childInode, ierr := fs.readInodeLocked(de.Inode)
					if ierr != nil {
						return fmt.Errorf("block=%d offset=%d: name=%q ReadInode(%d): %w",
							i, offset, de.Name, de.Inode, ierr)
					}
					actual := uint8(FTFile)
					switch {
					case childInode.IsDir():
						actual = FTDir
					case childInode.Mode == 0:
						return fmt.Errorf("block=%d offset=%d: name=%q inode=%d has Mode=0 (freed?)",
							i, offset, de.Name, de.Inode)
					}
					if de.FileType != actual {
						return fmt.Errorf("block=%d offset=%d: name=%q FileType=%d but inode=%d Mode=0x%x says actual=%d",
							i, offset, de.Name, de.FileType, de.Inode, childInode.Mode, actual)
					}
				}
			}
			offset += de.RecLen
		}
		if offset != uint16(fs.blockSize) {
			return fmt.Errorf("block=%d: RecLen sum=%d != blockSize=%d",
				i, offset, fs.blockSize)
		}
	}
	return nil
}
