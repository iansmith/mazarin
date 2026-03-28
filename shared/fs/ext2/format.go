package ext2

import (
	"fmt"
	"time"

	"mazzy/shared/blockdev"
)

// Format creates a minimal ext2 filesystem on the given block device.
// The device is assumed to be zeroed (e.g., a freshly created MemBlockDevice).
// Uses 4096-byte ext2 blocks, 1024 inodes per group, 128-byte inodes.
func Format(dev blockdev.BlockDevice, label string) error {
	const blockSize = 4096
	const logBlockSize = LogBlockSize4K
	const inodeSize = InodeSize128
	const inodesPerGroup = 1024
	const firstDataBlock = 0

	totalBytes := int64(dev.BlockSize()) * int64(dev.NumBlocks())
	totalBlocks := uint32(totalBytes / blockSize)

	if totalBlocks < 64 {
		return fmt.Errorf("ext2: device too small (%d blocks, need at least 64)", totalBlocks)
	}

	blocksPerGroup := uint32(8 * blockSize) // 32768
	if totalBlocks < blocksPerGroup {
		blocksPerGroup = totalBlocks
	}

	numGroups := (totalBlocks + blocksPerGroup - 1) / blocksPerGroup
	lastGroupBlocks := totalBlocks - (numGroups-1)*blocksPerGroup
	totalInodes := inodesPerGroup * numGroups

	now := uint32(time.Now().Unix())

	inodeTableBlocks := uint32((inodesPerGroup * inodeSize) / blockSize)
	if (inodesPerGroup*inodeSize)%blockSize != 0 {
		inodeTableBlocks++
	}

	gdtSize := numGroups * GroupDescSize
	gdtBlocks := (gdtSize + blockSize - 1) / blockSize

	// Build superblock.
	sb := Superblock{
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
		Magic:           Magic,
		State:           StateClean,
		Errors:          ErrorsContinue,
		LastCheck:        now,
		CheckInterval:   15552000,
		CreatorOS:       OSLinux,
		RevLevel:        RevDynamic,
		FirstIno:        InodeFirstNonRs,
		InodeSize:       inodeSize,
		FeatureIncompat: FeatureIncompatFileType,
		FeatureROCompat: FeatureROCompatSparseSuper,
	}
	copy(sb.VolumeName[:], label)

	// Build group descriptors and bitmaps.
	groups := make([]GroupDesc, numGroups)
	blockBitmaps := make([][]byte, numGroups)
	inodeBitmaps := make([][]byte, numGroups)

	for g := uint32(0); g < numGroups; g++ {
		groupStart := firstDataBlock + g*blocksPerGroup
		groupBlocks := blocksPerGroup
		if g == numGroups-1 {
			groupBlocks = lastGroupBlocks
		}

		hasSB := formatHasSuperblockBackup(g)
		metaStart := groupStart
		if hasSB {
			metaStart += 1 + gdtBlocks // skip superblock block + GDT blocks
		}

		gd := GroupDesc{
			BlockBitmap: metaStart,
			InodeBitmap: metaStart + 1,
			InodeTable:  metaStart + 2,
		}

		overhead := (gd.InodeTable + inodeTableBlocks) - groupStart

		// Block bitmap: mark metadata blocks as used.
		blockBitmap := make([]byte, blockSize)
		for b := uint32(0); b < overhead; b++ {
			blockBitmap[b/8] |= 1 << (b % 8)
		}
		// Padding: mark trailing bits beyond this group's block count.
		bitmapBits := uint32(blockSize) * 8
		for b := groupBlocks; b < bitmapBits; b++ {
			blockBitmap[b/8] |= 1 << (b % 8)
		}

		// Inode bitmap: mark reserved inodes in group 0.
		inodeBitmap := make([]byte, blockSize)
		if g == 0 {
			for i := uint32(0); i < InodeFirstNonRs-1; i++ {
				inodeBitmap[i/8] |= 1 << (i % 8)
			}
		}
		// Padding: mark trailing bits beyond inodesPerGroup.
		for b := uint32(inodesPerGroup); b < bitmapBits; b++ {
			inodeBitmap[b/8] |= 1 << (b % 8)
		}

		// Free counts.
		freeBlocks := groupBlocks - overhead
		freeInodes := uint32(inodesPerGroup)
		usedDirs := uint16(0)

		if g == 0 {
			freeInodes = inodesPerGroup - InodeFirstNonRs + 1
			freeBlocks-- // root dir data block
			usedDirs = 1
			// Mark root data block in bitmap.
			blockBitmap[overhead/8] |= 1 << (overhead % 8)
		}

		gd.FreeBlocksCount = uint16(freeBlocks)
		gd.FreeInodesCount = uint16(freeInodes)
		gd.UsedDirsCount = usedDirs

		groups[g] = gd
		blockBitmaps[g] = blockBitmap
		inodeBitmaps[g] = inodeBitmap
	}

	// Superblock free counts from group descriptors.
	totalFreeBlocks := uint32(0)
	totalFreeInodes := uint32(0)
	for g := uint32(0); g < numGroups; g++ {
		totalFreeBlocks += uint32(groups[g].FreeBlocksCount)
		totalFreeInodes += uint32(groups[g].FreeInodesCount)
	}
	sb.FreeBlocksCount = totalFreeBlocks
	sb.FreeInodesCount = totalFreeInodes

	// Write root inode (inode 2).
	rootDataBlock := groups[0].InodeTable + inodeTableBlocks
	rootInode := &Inode{
		Mode:       TypeDir | PermAll755,
		Size:       blockSize,
		ATime:      now,
		CTime:      now,
		MTime:      now,
		LinksCount: 2,
		Blocks:     blockSize / 512,
	}
	rootInode.Block[0] = rootDataBlock

	inodeOffset := int64(groups[0].InodeTable)*int64(blockSize) + int64(InodeRoot-1)*int64(inodeSize)
	inodeBuf := rootInode.Marshal()
	if err := formatWriteAt(dev, inodeOffset, inodeBuf[:]); err != nil {
		return fmt.Errorf("writing root inode: %w", err)
	}

	// Write root directory (. and .. entries).
	rootDirBuf := make([]byte, blockSize)
	dot := NewDirEntry(InodeRoot, ".", FTDir)
	n := MarshalDirEntry(rootDirBuf, dot)
	dotdot := NewDirEntry(InodeRoot, "..", FTDir)
	dotdot.RecLen = uint16(blockSize) - uint16(n)
	MarshalDirEntry(rootDirBuf[n:], dotdot)
	if err := formatWriteAt(dev, int64(rootDataBlock)*int64(blockSize), rootDirBuf); err != nil {
		return fmt.Errorf("writing root dir: %w", err)
	}

	// Flush bitmaps.
	for g := uint32(0); g < numGroups; g++ {
		bmOff := int64(groups[g].BlockBitmap) * int64(blockSize)
		if err := formatWriteAt(dev, bmOff, blockBitmaps[g]); err != nil {
			return fmt.Errorf("writing block bitmap group %d: %w", g, err)
		}
		imOff := int64(groups[g].InodeBitmap) * int64(blockSize)
		if err := formatWriteAt(dev, imOff, inodeBitmaps[g]); err != nil {
			return fmt.Errorf("writing inode bitmap group %d: %w", g, err)
		}
	}

	// Write superblock and GDT (with sparse_super backups).
	for g := uint32(0); g < numGroups; g++ {
		if !formatHasSuperblockBackup(g) {
			continue
		}
		groupStart := firstDataBlock + g*blocksPerGroup
		sbOffset := int64(groupStart)*int64(blockSize) + SuperblockOffset

		sbCopy := sb
		sbCopy.BlockGroupNr = uint16(g)
		sbData := sbCopy.Marshal()
		if err := formatWriteAt(dev, sbOffset, sbData[:]); err != nil {
			return fmt.Errorf("writing superblock group %d: %w", g, err)
		}

		gdtOffset := int64(groupStart+1) * int64(blockSize)
		for i := uint32(0); i < numGroups; i++ {
			gdBuf := groups[i].Marshal()
			off := gdtOffset + int64(i)*GroupDescSize
			if err := formatWriteAt(dev, off, gdBuf[:]); err != nil {
				return fmt.Errorf("writing GDT entry %d group %d: %w", i, g, err)
			}
		}
	}

	return nil
}

// formatWriteAt writes data at a byte offset to the device, handling alignment
// to the device's block size via read-modify-write for partial blocks.
func formatWriteAt(dev blockdev.BlockDevice, offset int64, data []byte) error {
	bs := int64(dev.BlockSize())
	for len(data) > 0 {
		lba := uint64(offset / bs)
		off := int(offset % bs)

		if off == 0 && int64(len(data)) >= bs {
			// Aligned full-block write.
			if err := dev.WriteBlock(lba, data[:bs]); err != nil {
				return err
			}
			data = data[bs:]
			offset += bs
		} else {
			// Partial block — read-modify-write.
			buf := make([]byte, bs)
			_ = dev.ReadBlock(lba, buf) // OK if device is zeroed
			n := int(bs) - off
			if n > len(data) {
				n = len(data)
			}
			copy(buf[off:], data[:n])
			if err := dev.WriteBlock(lba, buf); err != nil {
				return err
			}
			data = data[n:]
			offset += int64(n)
		}
	}
	return nil
}

// formatHasSuperblockBackup returns true if the given block group should have
// a superblock and GDT backup. With sparse_super, backups are in groups
// 0, 1, and groups that are powers of 3, 5, or 7.
func formatHasSuperblockBackup(group uint32) bool {
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
