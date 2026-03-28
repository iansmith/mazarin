// Command mkext2 creates ext2 filesystem images.
//
// Usage:
//
//	mkext2 -o output.img -size 64                     # Create 64MB empty ext2 image
//	mkext2 -o output.img -size 64 file1 file2 ...     # Create image with files in root
//	mkext2 -o output.img -size 64 -dir /fonts=./path/ # Create image with subdirectory
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mazzy/shared/fs/ext2"
)

func main() {
	outFile := flag.String("o", "", "output image file (required)")
	sizeMB := flag.Int("size", 64, "image size in MB")
	label := flag.String("label", "mazzyfs", "volume label")
	var dirs dirFlags
	flag.Var(&dirs, "dir", "directory mapping /mount=hostpath (repeatable)")
	flag.Parse()

	if *outFile == "" {
		fmt.Fprintf(os.Stderr, "mkext2: -o flag is required\n")
		os.Exit(1)
	}
	if *sizeMB < 1 {
		fmt.Fprintf(os.Stderr, "mkext2: -size must be at least 1 MB\n")
		os.Exit(1)
	}

	if err := createImage(*outFile, *sizeMB, *label, flag.Args(), dirs); err != nil {
		fmt.Fprintf(os.Stderr, "mkext2: %v\n", err)
		os.Exit(1)
	}
}

// dirFlags implements flag.Value for repeatable -dir flags.
type dirFlags []dirMapping

type dirMapping struct {
	mountPoint string // e.g. "/fonts"
	hostPath   string // e.g. "./path/to/fonts"
}

func (d *dirFlags) String() string { return "" }
func (d *dirFlags) Set(val string) error {
	parts := strings.SplitN(val, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("expected /mount=hostpath, got %q", val)
	}
	*d = append(*d, dirMapping{mountPoint: parts[0], hostPath: parts[1]})
	return nil
}

// formatter holds in-memory state during ext2 image creation.
type formatter struct {
	f              *os.File
	sb             ext2.Superblock
	groups         []ext2.GroupDesc
	blockBitmaps   [][]byte // one bitmap per group
	inodeBitmaps   [][]byte // one bitmap per group
	blockSize      uint32
	inodesPerGroup uint32
	inodeTableBlks uint32
	gdtBlocks      uint32
	numGroups      uint32
	now            uint32
}

// createImage creates an ext2 filesystem image with optional files and directories.
func createImage(path string, sizeMB int, label string, files []string, dirs dirFlags) error {
	fm, err := newFormatter(path, sizeMB, label)
	if err != nil {
		return err
	}
	defer fm.f.Close()

	// Add root-level files
	for _, filePath := range files {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}
		name := filepath.Base(filePath)
		if err := fm.addFile(ext2.InodeRoot, name, data, fileMode(filePath)); err != nil {
			return fmt.Errorf("adding %s: %w", name, err)
		}
	}

	// Add directories with their contents
	for _, dm := range dirs {
		if err := fm.addHostDir(ext2.InodeRoot, dm.mountPoint, dm.hostPath); err != nil {
			return fmt.Errorf("adding dir %s=%s: %w", dm.mountPoint, dm.hostPath, err)
		}
	}

	return fm.flush()
}

// fileMode returns ext2 permissions based on host file mode.
func fileMode(path string) uint16 {
	info, err := os.Stat(path)
	if err != nil {
		return ext2.PermAll644
	}
	if info.Mode()&0111 != 0 {
		return ext2.PermAll755
	}
	return ext2.PermAll644
}

// newFormatter creates a new formatter and initializes the empty filesystem.
func newFormatter(path string, sizeMB int, label string) (*formatter, error) {
	const blockSize = 4096
	const logBlockSize = ext2.LogBlockSize4K
	const inodeSize = ext2.InodeSize128
	const inodesPerGroup = 1024

	totalBytes := int64(sizeMB) * 1024 * 1024
	totalBlocks := uint32(totalBytes / blockSize)
	const firstDataBlock = 0

	blocksPerGroup := uint32(8 * blockSize) // 32768
	if totalBlocks < blocksPerGroup {
		blocksPerGroup = totalBlocks
	}

	numGroups := (totalBlocks + blocksPerGroup - 1) / blocksPerGroup
	lastGroupBlocks := totalBlocks - (numGroups-1)*blocksPerGroup
	totalInodes := inodesPerGroup * numGroups

	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return nil, fmt.Errorf("generating UUID: %w", err)
	}
	uuid[6] = (uuid[6] & 0x0F) | 0x40
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	now := uint32(time.Now().Unix())

	inodeTableBlocks := uint32((inodesPerGroup * inodeSize) / blockSize)
	if (inodesPerGroup*inodeSize)%blockSize != 0 {
		inodeTableBlocks++
	}

	gdtSize := numGroups * ext2.GroupDescSize
	gdtBlocks := (gdtSize + blockSize - 1) / blockSize

	fm := &formatter{
		blockSize:      blockSize,
		inodesPerGroup: inodesPerGroup,
		inodeTableBlks: inodeTableBlocks,
		gdtBlocks:      gdtBlocks,
		numGroups:      numGroups,
		now:            now,
		groups:         make([]ext2.GroupDesc, numGroups),
		blockBitmaps:   make([][]byte, numGroups),
		inodeBitmaps:   make([][]byte, numGroups),
	}

	fm.sb = ext2.Superblock{
		InodesCount:     totalInodes,
		BlocksCount:     totalBlocks,
		RBlocksCount:    totalBlocks / 20,
		FirstDataBlock:  firstDataBlock,
		LogBlockSize:    logBlockSize,
		LogFragSize:     logBlockSize,
		BlocksPerGroup:  blocksPerGroup,
		FragsPerGroup:   blocksPerGroup,
		InodesPerGroup:  inodesPerGroup,
		WTime:           now,
		MaxMntCount:     20,
		Magic:           ext2.Magic,
		State:           ext2.StateClean,
		Errors:          ext2.ErrorsContinue,
		LastCheck:        now,
		CheckInterval:   15552000,
		CreatorOS:       ext2.OSLinux,
		RevLevel:        ext2.RevDynamic,
		FirstIno:        ext2.InodeFirstNonRs,
		InodeSize:       inodeSize,
		FeatureIncompat: ext2.FeatureIncompatFileType,
		FeatureROCompat: ext2.FeatureROCompatSparseSuper,
		UUID:            uuid,
	}
	copy(fm.sb.VolumeName[:], label)

	// Create the image file
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	fm.f = f

	if err := f.Truncate(totalBytes); err != nil {
		return nil, fmt.Errorf("truncating to %d bytes: %w", totalBytes, err)
	}

	// Initialize group descriptors and bitmaps
	for g := uint32(0); g < numGroups; g++ {
		groupStart := firstDataBlock + g*blocksPerGroup
		groupBlocks := blocksPerGroup
		if g == numGroups-1 {
			groupBlocks = lastGroupBlocks
		}

		hasSB := hasSuperblockBackup(g)
		metaStart := groupStart
		if hasSB {
			metaStart += 1 + gdtBlocks
		}

		gd := ext2.GroupDesc{
			BlockBitmap: metaStart,
			InodeBitmap: metaStart + 1,
			InodeTable:  metaStart + 2,
		}

		overhead := (gd.InodeTable + inodeTableBlocks) - groupStart

		// Initialize block bitmap
		blockBitmap := make([]byte, blockSize)
		// Mark metadata blocks as used
		for b := uint32(0); b < overhead; b++ {
			blockBitmap[b/8] |= 1 << (b % 8)
		}
		// Padding: trailing bits beyond this group's actual block count
		bitmapBits := uint32(blockSize) * 8
		for b := groupBlocks; b < bitmapBits; b++ {
			blockBitmap[b/8] |= 1 << (b % 8)
		}

		// Initialize inode bitmap
		inodeBitmap := make([]byte, blockSize)
		if g == 0 {
			// Mark reserved inodes (1 through InodeFirstNonRs-1)
			for i := uint32(0); i < ext2.InodeFirstNonRs-1; i++ {
				inodeBitmap[i/8] |= 1 << (i % 8)
			}
		}
		// Padding: trailing bits beyond inodesPerGroup
		for b := uint32(inodesPerGroup); b < bitmapBits; b++ {
			inodeBitmap[b/8] |= 1 << (b % 8)
		}

		// Free counts
		freeBlocks := groupBlocks - overhead
		freeInodes := uint32(inodesPerGroup)
		usedDirs := uint16(0)

		if g == 0 {
			freeInodes = inodesPerGroup - ext2.InodeFirstNonRs + 1
			freeBlocks-- // root dir data block
			usedDirs = 1
			// Mark root data block in bitmap
			blockBitmap[overhead/8] |= 1 << (overhead % 8)
		}

		gd.FreeBlocksCount = uint16(freeBlocks)
		gd.FreeInodesCount = uint16(freeInodes)
		gd.UsedDirsCount = usedDirs

		fm.groups[g] = gd
		fm.blockBitmaps[g] = blockBitmap
		fm.inodeBitmaps[g] = inodeBitmap
	}

	// Compute superblock free counts from group descriptors
	totalFreeBlocks := uint32(0)
	totalFreeInodes := uint32(0)
	for g := uint32(0); g < numGroups; g++ {
		totalFreeBlocks += uint32(fm.groups[g].FreeBlocksCount)
		totalFreeInodes += uint32(fm.groups[g].FreeInodesCount)
	}
	fm.sb.FreeBlocksCount = totalFreeBlocks
	fm.sb.FreeInodesCount = totalFreeInodes

	// Write root inode and root directory
	rootDataBlock := fm.groups[0].InodeTable + inodeTableBlocks
	rootInode := &ext2.Inode{
		Mode:       ext2.TypeDir | ext2.PermAll755,
		Size:       blockSize,
		ATime:      now,
		CTime:      now,
		MTime:      now,
		LinksCount: 2,
		Blocks:     blockSize / 512,
	}
	rootInode.Block[0] = rootDataBlock
	if err := fm.writeInode(ext2.InodeRoot, rootInode); err != nil {
		return nil, fmt.Errorf("writing root inode: %w", err)
	}

	rootDirBuf := make([]byte, blockSize)
	dot := ext2.NewDirEntry(ext2.InodeRoot, ".", ext2.FTDir)
	n := ext2.MarshalDirEntry(rootDirBuf, dot)
	dotdot := ext2.NewDirEntry(ext2.InodeRoot, "..", ext2.FTDir)
	dotdot.RecLen = uint16(blockSize) - uint16(n)
	ext2.MarshalDirEntry(rootDirBuf[n:], dotdot)
	if err := fm.writeBlock(rootDataBlock, rootDirBuf); err != nil {
		return nil, fmt.Errorf("writing root dir: %w", err)
	}

	return fm, nil
}

// allocBlock finds and allocates a free block, returning its absolute block number.
func (fm *formatter) allocBlock() (uint32, error) {
	for g := uint32(0); g < fm.numGroups; g++ {
		if fm.groups[g].FreeBlocksCount == 0 {
			continue
		}
		bm := fm.blockBitmaps[g]
		groupStart := fm.sb.FirstDataBlock + g*fm.sb.BlocksPerGroup
		for byteIdx := 0; byteIdx < len(bm); byteIdx++ {
			if bm[byteIdx] == 0xFF {
				continue
			}
			for bit := uint32(0); bit < 8; bit++ {
				if bm[byteIdx]&(1<<bit) == 0 {
					bm[byteIdx] |= 1 << bit
					fm.groups[g].FreeBlocksCount--
					fm.sb.FreeBlocksCount--
					return groupStart + uint32(byteIdx)*8 + bit, nil
				}
			}
		}
	}
	return 0, ext2.ErrNoSpace
}

// allocInode finds and allocates a free inode, returning its inode number (1-based).
func (fm *formatter) allocInode() (uint32, error) {
	for g := uint32(0); g < fm.numGroups; g++ {
		if fm.groups[g].FreeInodesCount == 0 {
			continue
		}
		bm := fm.inodeBitmaps[g]
		for byteIdx := uint32(0); byteIdx < fm.inodesPerGroup/8; byteIdx++ {
			if bm[byteIdx] == 0xFF {
				continue
			}
			for bit := uint32(0); bit < 8; bit++ {
				if bm[byteIdx]&(1<<bit) == 0 {
					bm[byteIdx] |= 1 << bit
					fm.groups[g].FreeInodesCount--
					fm.sb.FreeInodesCount--
					return g*fm.inodesPerGroup + byteIdx*8 + bit + 1, nil
				}
			}
		}
	}
	return 0, ext2.ErrNoInodes
}

// writeInode writes an inode to the inode table on disk.
func (fm *formatter) writeInode(inum uint32, inode *ext2.Inode) error {
	g := (inum - 1) / fm.inodesPerGroup
	idx := (inum - 1) % fm.inodesPerGroup
	offset := int64(fm.groups[g].InodeTable)*int64(fm.blockSize) + int64(idx)*int64(fm.sb.InodeSize)
	buf := inode.Marshal()
	_, err := fm.f.WriteAt(buf[:], offset)
	return err
}

// readInode reads an inode from the inode table on disk.
func (fm *formatter) readInode(inum uint32) (*ext2.Inode, error) {
	g := (inum - 1) / fm.inodesPerGroup
	idx := (inum - 1) % fm.inodesPerGroup
	offset := int64(fm.groups[g].InodeTable)*int64(fm.blockSize) + int64(idx)*int64(fm.sb.InodeSize)
	var buf [ext2.InodeSize128]byte
	if _, err := fm.f.ReadAt(buf[:], offset); err != nil {
		return nil, err
	}
	return ext2.UnmarshalInode(buf[:])
}

// writeBlock writes a full block to disk.
func (fm *formatter) writeBlock(blockNum uint32, data []byte) error {
	_, err := fm.f.WriteAt(data[:fm.blockSize], int64(blockNum)*int64(fm.blockSize))
	return err
}

// readBlock reads a full block from disk.
func (fm *formatter) readBlock(blockNum uint32) ([]byte, error) {
	buf := make([]byte, fm.blockSize)
	_, err := fm.f.ReadAt(buf, int64(blockNum)*int64(fm.blockSize))
	return buf, err
}

// addDirEntry adds a directory entry to the directory owned by parentInum.
// It reads the directory's data blocks, finds space in the last entry's
// padding, and inserts the new entry.
func (fm *formatter) addDirEntry(parentInum uint32, name string, childInum uint32, fileType uint8) error {
	parent, err := fm.readInode(parentInum)
	if err != nil {
		return err
	}

	needed := ext2.DirEntryRealSize(len(name))

	// Walk the directory's data blocks looking for space.
	// We scan the block with the last entry and try to shrink its RecLen.
	blocksUsed := parent.Size / fm.blockSize
	for i := uint32(0); i < blocksUsed; i++ {
		blockNum, err := fm.inodeBlockNum(parent, i)
		if err != nil {
			return err
		}
		dirData, err := fm.readBlock(blockNum)
		if err != nil {
			return err
		}

		offset := uint16(0)
		for offset < uint16(fm.blockSize) {
			de, _, err := ext2.UnmarshalDirEntry(dirData[offset:])
			if err != nil {
				return err
			}
			actualSize := ext2.DirEntryRealSize(int(de.NameLen))
			slack := de.RecLen - actualSize

			if slack >= needed {
				// Shrink the current entry and insert the new one after it
				oldRecLen := de.RecLen
				de.RecLen = actualSize
				ext2.MarshalDirEntry(dirData[offset:], de)

				newEntry := ext2.NewDirEntry(childInum, name, fileType)
				newEntry.RecLen = oldRecLen - actualSize
				ext2.MarshalDirEntry(dirData[offset+actualSize:], newEntry)

				return fm.writeBlock(blockNum, dirData)
			}

			offset += de.RecLen
		}
	}

	// No space in existing blocks — allocate a new directory block.
	newBlock, err := fm.allocBlock()
	if err != nil {
		return err
	}

	dirData := make([]byte, fm.blockSize)
	newEntry := ext2.NewDirEntry(childInum, name, fileType)
	newEntry.RecLen = uint16(fm.blockSize) // fill entire block
	ext2.MarshalDirEntry(dirData, newEntry)

	if err := fm.writeBlock(newBlock, dirData); err != nil {
		return err
	}

	// Add block to parent inode
	blockIdx := parent.Size / fm.blockSize
	if err := fm.setInodeBlock(parent, blockIdx, newBlock); err != nil {
		return err
	}
	parent.Size += fm.blockSize
	parent.Blocks += fm.blockSize / 512
	return fm.writeInode(parentInum, parent)
}

// inodeBlockNum returns the absolute block number for the nth data block of an inode.
func (fm *formatter) inodeBlockNum(inode *ext2.Inode, n uint32) (uint32, error) {
	ptrsPerBlock := fm.blockSize / 4

	// Direct blocks (0-11)
	if n < ext2.NDirect {
		return inode.Block[n], nil
	}
	n -= ext2.NDirect

	// Single indirect (next ptrsPerBlock entries)
	if n < ptrsPerBlock {
		if inode.Block[ext2.IndirectBlock] == 0 {
			return 0, ext2.ErrCorrupted
		}
		return fm.readBlockPtr(inode.Block[ext2.IndirectBlock], n)
	}
	n -= ptrsPerBlock

	// Double indirect (next ptrsPerBlock^2 entries)
	if n < ptrsPerBlock*ptrsPerBlock {
		if inode.Block[ext2.DblIndirectBlock] == 0 {
			return 0, ext2.ErrCorrupted
		}
		idx1 := n / ptrsPerBlock
		idx2 := n % ptrsPerBlock
		indBlock, err := fm.readBlockPtr(inode.Block[ext2.DblIndirectBlock], idx1)
		if err != nil {
			return 0, err
		}
		return fm.readBlockPtr(indBlock, idx2)
	}

	return 0, fmt.Errorf("file too large for ext2 (block index %d)", n+ext2.NDirect+ptrsPerBlock+ptrsPerBlock*ptrsPerBlock)
}

// setInodeBlock sets the nth data block pointer in an inode, allocating
// indirect blocks as needed.
func (fm *formatter) setInodeBlock(inode *ext2.Inode, n uint32, blockNum uint32) error {
	ptrsPerBlock := fm.blockSize / 4

	// Direct blocks
	if n < ext2.NDirect {
		inode.Block[n] = blockNum
		return nil
	}
	n -= ext2.NDirect

	// Single indirect
	if n < ptrsPerBlock {
		if inode.Block[ext2.IndirectBlock] == 0 {
			indBlock, err := fm.allocBlock()
			if err != nil {
				return err
			}
			// Zero the indirect block
			if err := fm.writeBlock(indBlock, make([]byte, fm.blockSize)); err != nil {
				return err
			}
			inode.Block[ext2.IndirectBlock] = indBlock
			inode.Blocks += fm.blockSize / 512
		}
		return fm.writeBlockPtr(inode.Block[ext2.IndirectBlock], n, blockNum)
	}
	n -= ptrsPerBlock

	// Double indirect
	if n < ptrsPerBlock*ptrsPerBlock {
		if inode.Block[ext2.DblIndirectBlock] == 0 {
			dblBlock, err := fm.allocBlock()
			if err != nil {
				return err
			}
			if err := fm.writeBlock(dblBlock, make([]byte, fm.blockSize)); err != nil {
				return err
			}
			inode.Block[ext2.DblIndirectBlock] = dblBlock
			inode.Blocks += fm.blockSize / 512
		}
		idx1 := n / ptrsPerBlock
		idx2 := n % ptrsPerBlock

		// Read/alloc the second-level indirect block
		indBlock, err := fm.readBlockPtr(inode.Block[ext2.DblIndirectBlock], idx1)
		if err != nil {
			return err
		}
		if indBlock == 0 {
			indBlock, err = fm.allocBlock()
			if err != nil {
				return err
			}
			if err := fm.writeBlock(indBlock, make([]byte, fm.blockSize)); err != nil {
				return err
			}
			if err := fm.writeBlockPtr(inode.Block[ext2.DblIndirectBlock], idx1, indBlock); err != nil {
				return err
			}
			inode.Blocks += fm.blockSize / 512
		}
		return fm.writeBlockPtr(indBlock, idx2, blockNum)
	}

	return fmt.Errorf("file too large for ext2")
}

// readBlockPtr reads a uint32 block pointer at index idx from a block of pointers.
func (fm *formatter) readBlockPtr(blockNum uint32, idx uint32) (uint32, error) {
	offset := int64(blockNum)*int64(fm.blockSize) + int64(idx)*4
	var buf [4]byte
	if _, err := fm.f.ReadAt(buf[:], offset); err != nil {
		return 0, err
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, nil
}

// writeBlockPtr writes a uint32 block pointer at index idx in a block of pointers.
func (fm *formatter) writeBlockPtr(blockNum uint32, idx uint32, val uint32) error {
	offset := int64(blockNum)*int64(fm.blockSize) + int64(idx)*4
	buf := [4]byte{byte(val), byte(val >> 8), byte(val >> 16), byte(val >> 24)}
	_, err := fm.f.WriteAt(buf[:], offset)
	return err
}

// addFile creates a regular file in the given parent directory.
func (fm *formatter) addFile(parentInum uint32, name string, data []byte, perm uint16) error {
	inum, err := fm.allocInode()
	if err != nil {
		return err
	}

	inode := &ext2.Inode{
		Mode:       ext2.TypeFile | perm,
		Size:       uint32(len(data)),
		ATime:      fm.now,
		CTime:      fm.now,
		MTime:      fm.now,
		LinksCount: 1,
	}

	// Write data blocks
	fullBlocks := uint32(len(data)) / fm.blockSize
	remaining := uint32(len(data)) % fm.blockSize
	totalDataBlocks := fullBlocks
	if remaining > 0 {
		totalDataBlocks++
	}

	for i := uint32(0); i < totalDataBlocks; i++ {
		block, err := fm.allocBlock()
		if err != nil {
			return err
		}
		if err := fm.setInodeBlock(inode, i, block); err != nil {
			return err
		}

		start := i * fm.blockSize
		end := start + fm.blockSize
		if end > uint32(len(data)) {
			// Last partial block — write padded
			padded := make([]byte, fm.blockSize)
			copy(padded, data[start:])
			if err := fm.writeBlock(block, padded); err != nil {
				return err
			}
		} else {
			if err := fm.writeBlock(block, data[start:end]); err != nil {
				return err
			}
		}
		inode.Blocks += fm.blockSize / 512
	}

	if err := fm.writeInode(inum, inode); err != nil {
		return err
	}

	return fm.addDirEntry(parentInum, name, inum, ext2.FTFile)
}

// addDir creates a subdirectory in the given parent directory and returns its inode number.
func (fm *formatter) addDir(parentInum uint32, name string) (uint32, error) {
	inum, err := fm.allocInode()
	if err != nil {
		return 0, err
	}

	dataBlock, err := fm.allocBlock()
	if err != nil {
		return 0, err
	}

	inode := &ext2.Inode{
		Mode:       ext2.TypeDir | ext2.PermAll755,
		Size:       fm.blockSize,
		ATime:      fm.now,
		CTime:      fm.now,
		MTime:      fm.now,
		LinksCount: 2, // . and parent's entry
		Blocks:     fm.blockSize / 512,
	}
	inode.Block[0] = dataBlock

	// Write . and .. entries
	dirData := make([]byte, fm.blockSize)
	dot := ext2.NewDirEntry(inum, ".", ext2.FTDir)
	n := ext2.MarshalDirEntry(dirData, dot)
	dotdot := ext2.NewDirEntry(parentInum, "..", ext2.FTDir)
	dotdot.RecLen = uint16(fm.blockSize) - uint16(n)
	ext2.MarshalDirEntry(dirData[n:], dotdot)

	if err := fm.writeBlock(dataBlock, dirData); err != nil {
		return 0, err
	}
	if err := fm.writeInode(inum, inode); err != nil {
		return 0, err
	}

	// Add entry to parent
	if err := fm.addDirEntry(parentInum, name, inum, ext2.FTDir); err != nil {
		return 0, err
	}

	// Increment parent's link count (for ..)
	parent, err := fm.readInode(parentInum)
	if err != nil {
		return 0, err
	}
	parent.LinksCount++
	if err := fm.writeInode(parentInum, parent); err != nil {
		return 0, err
	}

	// Update group's UsedDirsCount
	g := (inum - 1) / fm.inodesPerGroup
	fm.groups[g].UsedDirsCount++

	return inum, nil
}

// addHostDir creates a subdirectory tree from a host directory.
// mountPoint is like "/fonts", hostPath is the local directory to copy from.
func (fm *formatter) addHostDir(parentInum uint32, mountPoint string, hostPath string) error {
	// Strip leading slashes and split path components
	mountPoint = strings.TrimPrefix(mountPoint, "/")
	parts := strings.Split(mountPoint, "/")

	// Create the directory chain
	currentInum := parentInum
	for _, part := range parts {
		if part == "" {
			continue
		}
		inum, err := fm.addDir(currentInum, part)
		if err != nil {
			return err
		}
		currentInum = inum
	}

	// Walk the host directory and add all files
	entries, err := os.ReadDir(hostPath)
	if err != nil {
		return fmt.Errorf("reading host dir %s: %w", hostPath, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(hostPath, entry.Name())
		if entry.IsDir() {
			// Recurse into subdirectories
			subInum, err := fm.addDir(currentInum, entry.Name())
			if err != nil {
				return err
			}
			if err := fm.addHostDirContents(subInum, entryPath); err != nil {
				return err
			}
		} else if entry.Type().IsRegular() {
			data, err := os.ReadFile(entryPath)
			if err != nil {
				return fmt.Errorf("reading %s: %w", entryPath, err)
			}
			if err := fm.addFile(currentInum, entry.Name(), data, fileMode(entryPath)); err != nil {
				return err
			}
		}
		// Skip symlinks, devices, etc. for now
	}

	return nil
}

// addHostDirContents adds the contents of a host directory into an existing ext2 directory.
func (fm *formatter) addHostDirContents(parentInum uint32, hostPath string) error {
	entries, err := os.ReadDir(hostPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := filepath.Join(hostPath, entry.Name())
		if entry.IsDir() {
			subInum, err := fm.addDir(parentInum, entry.Name())
			if err != nil {
				return err
			}
			if err := fm.addHostDirContents(subInum, entryPath); err != nil {
				return err
			}
		} else if entry.Type().IsRegular() {
			data, err := os.ReadFile(entryPath)
			if err != nil {
				return err
			}
			if err := fm.addFile(parentInum, entry.Name(), data, fileMode(entryPath)); err != nil {
				return err
			}
		}
	}
	return nil
}

// flush writes all in-memory metadata (bitmaps, group descriptors, superblock)
// to disk. Must be called after all files/dirs are added.
func (fm *formatter) flush() error {
	// Write bitmaps
	for g := uint32(0); g < fm.numGroups; g++ {
		gd := &fm.groups[g]
		if err := fm.writeBlock(gd.BlockBitmap, fm.blockBitmaps[g]); err != nil {
			return fmt.Errorf("writing block bitmap for group %d: %w", g, err)
		}
		if err := fm.writeBlock(gd.InodeBitmap, fm.inodeBitmaps[g]); err != nil {
			return fmt.Errorf("writing inode bitmap for group %d: %w", g, err)
		}
	}

	// Write superblock and GDT (with backups)
	for g := uint32(0); g < fm.numGroups; g++ {
		if !hasSuperblockBackup(g) {
			continue
		}
		groupStart := fm.sb.FirstDataBlock + g*fm.sb.BlocksPerGroup
		sbOffset := int64(groupStart)*int64(fm.blockSize) + ext2.SuperblockOffset

		sbCopy := fm.sb
		sbCopy.BlockGroupNr = uint16(g)
		sbData := sbCopy.Marshal()

		if _, err := fm.f.WriteAt(sbData[:], sbOffset); err != nil {
			return fmt.Errorf("writing superblock for group %d: %w", g, err)
		}

		gdtOffset := int64(groupStart+1) * int64(fm.blockSize)
		for i := uint32(0); i < fm.numGroups; i++ {
			gdBuf := fm.groups[i].Marshal()
			off := gdtOffset + int64(i)*ext2.GroupDescSize
			if _, err := fm.f.WriteAt(gdBuf[:], off); err != nil {
				return fmt.Errorf("writing GDT entry %d for group %d: %w", i, g, err)
			}
		}
	}

	return nil
}

// hasSuperblockBackup returns true if the given block group should have
// a superblock and GDT backup. With sparse_super, backups are in groups
// 0, 1, and groups that are powers of 3, 5, or 7.
func hasSuperblockBackup(group uint32) bool {
	if group == 0 || group == 1 {
		return true
	}
	for _, base := range []uint32{3, 5, 7} {
		n := base
		for n < group {
			n *= base
		}
		if n == group {
			return true
		}
	}
	return false
}
