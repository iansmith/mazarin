package ext2

import "encoding/binary"

// ReadAt reads len(p) bytes from the file at byte offset off,
// io.ReaderAt-style: it neither consults nor advances the file position, and
// concurrent ReadAt calls on the same File are independent (no File field is
// mutated — all scratch state is local). Returns the number of bytes read; a
// read starting at or past EOF returns (0, ErrEndOfFile), and a read that
// runs into EOF returns the bytes read with a nil error.
//
// The device cost is bounded at three round trips per call regardless of
// window size or indirection depth (MAZ-165): one batched read for every
// indirect table the window needs at level 1 (the single-indirect table
// and/or the double-indirect L1), one batched read for all distinct L2
// tables, and one batched read for all non-sparse data blocks. Phases with
// nothing to fetch are skipped, so small direct-range reads cost a single
// round trip.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	if f.inode.IsDir() {
		return 0, ErrNotFile
	}
	if off < 0 {
		return 0, ErrReadFailed
	}
	fileSize := uint64(f.inode.Size)
	if uint64(off) >= fileSize {
		return 0, ErrEndOfFile
	}
	n := uint64(len(p))
	if remain := fileSize - uint64(off); n > remain {
		n = remain
	}
	if n == 0 {
		return 0, nil
	}

	bs := uint64(f.fs.blockSize)
	firstBlk := uint32(uint64(off) / bs)
	lastBlk := uint32((uint64(off) + n - 1) / bs)
	count := lastBlk - firstBlk + 1

	blockNums, err := f.fs.resolveBlockRangeBatched(&f.inode, firstBlk, count)
	if err != nil {
		return 0, err
	}

	// Read every non-sparse block in one readBlocks call into a zero-filled
	// staging buffer, so sparse blocks read as zeros.
	staging := make([]byte, uint64(count)*bs)
	physBlocks := make([]uint32, 0, count)
	windowIdx := make([]uint32, 0, count) // window index of each, parallel to physBlocks
	for i, bn := range blockNums {
		if bn != 0 {
			physBlocks = append(physBlocks, bn)
			windowIdx = append(windowIdx, uint32(i))
		}
	}
	switch {
	case len(physBlocks) == int(count):
		// No holes: readBlocks' contiguous layout is already the window
		// layout, so it can land straight in staging.
		if err := f.fs.readBlocks(physBlocks, staging); err != nil {
			return 0, err
		}
	case len(physBlocks) > 0:
		compact := make([]byte, uint64(len(physBlocks))*bs)
		if err := f.fs.readBlocks(physBlocks, compact); err != nil {
			return 0, err
		}
		for i, pos := range windowIdx {
			copy(staging[uint64(pos)*bs:], compact[uint64(i)*bs:uint64(i+1)*bs])
		}
	}

	skew := uint64(off) % bs
	copy(p[:n], staging[skew:skew+n])
	return int(n), nil
}

// resolveBlockRangeBatched resolves the physical block numbers for data
// blocks startBlock..startBlock+count-1 with batched metadata I/O: every
// level-1 table the range touches (the single-indirect table and/or the
// double-indirect L1) is fetched in one readBlocks call, then every distinct
// L2 table in a second. Contrast resolveBlockListLocked, which reads tables
// one at a time as the walk encounters them (1 + #L2s round trips).
// Caller must hold fs.mu.
func (fs *FileSystem) resolveBlockRangeBatched(inode *Inode, startBlock, count uint32) ([]uint32, error) {
	ptrsPerBlock := fs.blockSize / 4
	bs := int(fs.blockSize)

	// Region bounds (block indices within the file).
	singleEnd := NDirect + ptrsPerBlock
	doubleEnd := singleEnd + ptrsPerBlock*ptrsPerBlock
	if startBlock+count > doubleEnd {
		return nil, ErrCorrupted // triple-indirect unsupported, as elsewhere
	}
	end := startBlock + count

	needSingle := startBlock < singleEnd && end > NDirect && inode.Block[IndirectBlock] != 0
	needDouble := end > singleEnd && inode.Block[DblIndirectBlock] != 0

	// Phase 1 — level-1 tables in one batch.
	var l1Single, l1Double []byte
	var l1Blocks []uint32
	if needSingle {
		l1Blocks = append(l1Blocks, inode.Block[IndirectBlock])
	}
	if needDouble {
		l1Blocks = append(l1Blocks, inode.Block[DblIndirectBlock])
	}
	if len(l1Blocks) > 0 {
		buf := make([]byte, len(l1Blocks)*bs)
		if err := fs.readBlocks(l1Blocks, buf); err != nil {
			return nil, err
		}
		if needSingle {
			l1Single = buf[:bs]
			buf = buf[bs:]
		}
		if needDouble {
			l1Double = buf[:bs]
		}
	}

	// Phase 2 — distinct L2 tables in one batch.
	var l2ByBlock map[uint32][]byte
	if needDouble {
		firstIdx1 := uint32(0)
		if startBlock > singleEnd {
			firstIdx1 = (startBlock - singleEnd) / ptrsPerBlock
		}
		lastIdx1 := (end - 1 - singleEnd) / ptrsPerBlock
		var l2Blocks []uint32
		for idx1 := firstIdx1; idx1 <= lastIdx1; idx1++ {
			if bn := binary.LittleEndian.Uint32(l1Double[idx1*4:]); bn != 0 {
				l2Blocks = append(l2Blocks, bn)
			}
		}
		if len(l2Blocks) > 0 {
			buf := make([]byte, len(l2Blocks)*bs)
			if err := fs.readBlocks(l2Blocks, buf); err != nil {
				return nil, err
			}
			l2ByBlock = make(map[uint32][]byte, len(l2Blocks))
			for i, bn := range l2Blocks {
				l2ByBlock[bn] = buf[i*bs : (i+1)*bs]
			}
		}
	}

	// Assemble the data-block list from the cached tables.
	blocks := make([]uint32, 0, count)
	for i := range count {
		n := startBlock + i
		switch {
		case n < NDirect:
			blocks = append(blocks, inode.Block[n])
		case n < singleEnd:
			if l1Single == nil {
				blocks = append(blocks, 0)
				continue
			}
			blocks = append(blocks, binary.LittleEndian.Uint32(l1Single[(n-NDirect)*4:]))
		default:
			if l1Double == nil {
				blocks = append(blocks, 0)
				continue
			}
			adj := n - singleEnd
			l2 := l2ByBlock[binary.LittleEndian.Uint32(l1Double[adj/ptrsPerBlock*4:])]
			if l2 == nil {
				blocks = append(blocks, 0)
				continue
			}
			blocks = append(blocks, binary.LittleEndian.Uint32(l2[adj%ptrsPerBlock*4:]))
		}
	}
	return blocks, nil
}
