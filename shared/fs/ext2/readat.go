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
	staging, err := f.fs.readWindowBlocks(blockNums)
	if err != nil {
		return 0, err
	}

	skew := uint64(off) % bs
	copy(p[:n], staging[skew:skew+n])
	return int(n), nil
}

// readWindowBlocks reads every non-sparse block in blockNums with one batched
// readBlocks call and returns the window's bytes with sparse blocks
// zero-filled. Caller must hold fs.mu.
func (fs *FileSystem) readWindowBlocks(blockNums []uint32) ([]byte, error) {
	bs := uint64(fs.blockSize)
	staging := make([]byte, uint64(len(blockNums))*bs)
	physBlocks := make([]uint32, 0, len(blockNums))
	windowIdx := make([]uint32, 0, len(blockNums)) // window index of each, parallel to physBlocks
	for i, bn := range blockNums {
		if bn != 0 {
			physBlocks = append(physBlocks, bn)
			windowIdx = append(windowIdx, uint32(i))
		}
	}
	switch {
	case len(physBlocks) == len(blockNums):
		// No holes: readBlocks' contiguous layout is already the window
		// layout, so it can land straight in staging.
		if err := fs.readBlocks(physBlocks, staging); err != nil {
			return nil, err
		}
	case len(physBlocks) > 0:
		compact := make([]byte, uint64(len(physBlocks))*bs)
		if err := fs.readBlocks(physBlocks, compact); err != nil {
			return nil, err
		}
		for i, pos := range windowIdx {
			copy(staging[uint64(pos)*bs:], compact[uint64(i)*bs:uint64(i+1)*bs])
		}
	}
	return staging, nil
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
	singleEnd := NDirect + ptrsPerBlock
	doubleEnd := singleEnd + ptrsPerBlock*ptrsPerBlock
	if startBlock+count > doubleEnd {
		return nil, ErrCorrupted // triple-indirect unsupported, as elsewhere
	}
	end := startBlock + count

	needSingle := startBlock < singleEnd && end > NDirect && inode.Block[IndirectBlock] != 0
	needDouble := end > singleEnd && inode.Block[DblIndirectBlock] != 0

	l1Single, l1Double, err := fs.fetchL1Tables(inode, needSingle, needDouble)
	if err != nil {
		return nil, err
	}
	l2ByBlock, err := fs.fetchL2Tables(l1Double, startBlock, end, singleEnd, ptrsPerBlock)
	if err != nil {
		return nil, err
	}
	return assembleBlockList(inode, l1Single, l1Double, l2ByBlock, startBlock, count, singleEnd, ptrsPerBlock), nil
}

// assembleBlockList maps each file block index in the range to its physical
// block number using the already-fetched in-memory tables (0 = sparse).
func assembleBlockList(inode *Inode, l1Single, l1Double []byte, l2ByBlock map[uint32][]byte, startBlock, count, singleEnd, ptrsPerBlock uint32) []uint32 {
	blocks := make([]uint32, 0, count)
	for i := range count {
		n := startBlock + i
		switch {
		case n < NDirect:
			blocks = append(blocks, inode.Block[n])
		case n < singleEnd:
			blocks = append(blocks, tablePtr(l1Single, n-NDirect))
		default:
			adj := n - singleEnd
			l2 := l2ByBlock[tablePtr(l1Double, adj/ptrsPerBlock)]
			blocks = append(blocks, tablePtr(l2, adj%ptrsPerBlock))
		}
	}
	return blocks
}

// fetchL1Tables reads the level-1 indirect tables the range needs — the
// single-indirect table and/or the double-indirect L1 — in one batched
// readBlocks call. Either return may be nil (table not needed or absent,
// meaning those blocks are sparse). Caller must hold fs.mu.
func (fs *FileSystem) fetchL1Tables(inode *Inode, needSingle, needDouble bool) (l1Single, l1Double []byte, err error) {
	bs := int(fs.blockSize)
	var l1Blocks []uint32
	if needSingle {
		l1Blocks = append(l1Blocks, inode.Block[IndirectBlock])
	}
	if needDouble {
		l1Blocks = append(l1Blocks, inode.Block[DblIndirectBlock])
	}
	if len(l1Blocks) == 0 {
		return nil, nil, nil
	}
	buf := make([]byte, len(l1Blocks)*bs)
	if err := fs.readBlocks(l1Blocks, buf); err != nil {
		return nil, nil, err
	}
	if needSingle {
		l1Single = buf[:bs]
		buf = buf[bs:]
	}
	if needDouble {
		l1Double = buf[:bs]
	}
	return l1Single, l1Double, nil
}

// fetchL2Tables reads every distinct L2 table the range's double-indirect
// span references, in one batched readBlocks call, keyed by table block
// number. Returns nil when the range has no double-indirect span (l1Double
// nil) or every referenced table pointer is sparse. Caller must hold fs.mu.
func (fs *FileSystem) fetchL2Tables(l1Double []byte, startBlock, end, singleEnd, ptrsPerBlock uint32) (map[uint32][]byte, error) {
	if l1Double == nil {
		return nil, nil
	}
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
	if len(l2Blocks) == 0 {
		return nil, nil
	}
	bs := int(fs.blockSize)
	buf := make([]byte, len(l2Blocks)*bs)
	if err := fs.readBlocks(l2Blocks, buf); err != nil {
		return nil, err
	}
	l2ByBlock := make(map[uint32][]byte, len(l2Blocks))
	for i, bn := range l2Blocks {
		l2ByBlock[bn] = buf[i*bs : (i+1)*bs]
	}
	return l2ByBlock, nil
}

// tablePtr reads the idx'th little-endian block pointer from an in-memory
// indirect table; a nil table (absent = sparse) yields 0, matching the
// sparse-block convention.
func tablePtr(table []byte, idx uint32) uint32 {
	if table == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(table[idx*4:])
}
