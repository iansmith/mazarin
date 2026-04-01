package ext2

import "time"

// ReadIntoTimings captures per-phase timing from ReadInto for diagnostics.
// Set ReadIntoTimingHook to a non-nil function to receive these after each call.
type ReadIntoTimings struct {
	Resolve  time.Duration
	Separate time.Duration
	Alloc    time.Duration
	DMA      time.Duration
	Copy     time.Duration
	Total    time.Duration
	Blocks   int
}

// ReadIntoTimingHook, if non-nil, is called after each ReadInto with phase timings.
var ReadIntoTimingHook func(ReadIntoTimings)

// ReadBlocksTimingHook, if non-nil, is called from readBlocks batch path with timing.
var ReadBlocksTimingHook func(blocks int, makeAlloc, loop, lbaBuild, readCall time.Duration)

// ReadBlocksPreHook, if non-nil, is called before readBlocks batch path (for tracing setup).
var ReadBlocksPreHook func(blocks int)

// ReadBlocksPostHook, if non-nil, is called after readBlocks batch path (for tracing teardown).
var ReadBlocksPostHook func(blocks int)

// File represents an open file on an ext2 filesystem.
type File struct {
	fs       *FileSystem
	inum     uint32
	inode    Inode
	name     string
	pos      uint64 // Current read position
	blockBuf []byte // Cached current block (lazily allocated)
	blockIdx int64  // Which data block index is cached (-1 = none)
}

// Read reads up to len(buf) bytes from the file at the current position.
func (f *File) Read(buf []byte) (int, error) {
	if f.inode.IsDir() {
		return 0, ErrNotFile
	}
	fileSize := uint64(f.inode.Size)
	if f.pos >= fileSize {
		return 0, ErrEndOfFile
	}

	blockSize := uint64(f.fs.blockSize)
	if f.blockBuf == nil {
		f.blockBuf = make([]byte, blockSize)
		f.blockIdx = -1
	}

	totalRead := 0
	for len(buf) > 0 && f.pos < fileSize {
		// Which data block and offset within it?
		blkIdx := int64(f.pos / blockSize)
		blkOff := f.pos % blockSize

		// Load block if not cached
		if blkIdx != f.blockIdx {
			blockNum, err := f.fs.inodeBlockNum(&f.inode, uint32(blkIdx))
			if err != nil {
				return totalRead, err
			}
			if blockNum == 0 {
				// Sparse block — zero fill
				for i := range f.blockBuf {
					f.blockBuf[i] = 0
				}
			} else {
				if err := f.fs.readBlock(blockNum, f.blockBuf); err != nil {
					return totalRead, err
				}
			}
			f.blockIdx = blkIdx
		}

		// How much to copy from this block?
		remaining := blockSize - blkOff
		remainFile := fileSize - f.pos
		toRead := uint64(len(buf))
		if toRead > remaining {
			toRead = remaining
		}
		if toRead > remainFile {
			toRead = remainFile
		}

		copy(buf[:toRead], f.blockBuf[blkOff:blkOff+toRead])
		buf = buf[toRead:]
		f.pos += toRead
		totalRead += int(toRead)
	}

	return totalRead, nil
}

// ReadAll reads the entire file and returns its contents.
func (f *File) ReadAll() ([]byte, error) {
	if f.inode.IsDir() {
		return nil, ErrNotFile
	}
	fileSize := int(f.inode.Size)
	if fileSize == 0 {
		return nil, nil
	}
	result := make([]byte, fileSize)
	n, err := f.ReadInto(result)
	if err != nil {
		return nil, err
	}
	return result[:n], nil
}

// ReadInto reads the entire file into the provided buffer using batched I/O.
// dst must be at least file.Size() bytes. Returns the number of bytes read.
func (f *File) ReadInto(dst []byte) (int, error) {
	if f.inode.IsDir() {
		return 0, ErrNotFile
	}
	fileSize := int(f.inode.Size)
	if fileSize == 0 {
		return 0, nil
	}
	if len(dst) < fileSize {
		return 0, ErrReadFailed
	}

	t0 := time.Now()

	blockSize := int(f.fs.blockSize)
	totalBlocks := uint32((fileSize + blockSize - 1) / blockSize)

	// Resolve all block numbers upfront.
	blockNums, err := f.fs.ResolveBlockList(&f.inode, 0, totalBlocks)
	if err != nil {
		return 0, err
	}
	tResolve := time.Since(t0)

	tSepStart := time.Now()
	// Separate zero (sparse) blocks from real blocks.
	// Build a list of non-zero blocks and track their positions.
	type blockPos struct {
		dstIndex int    // index in blockNums (position in output)
		blockNum uint32 // fs block number
	}
	nonZero := make([]blockPos, 0, len(blockNums))
	for i, bn := range blockNums {
		if bn == 0 {
			// Sparse block — zero-fill the region.
			off := i * blockSize
			end := off + blockSize
			if end > fileSize {
				end = fileSize
			}
			for j := off; j < end; j++ {
				dst[j] = 0
			}
		} else {
			nonZero = append(nonZero, blockPos{i, bn})
		}
	}

	if len(nonZero) == 0 {
		return fileSize, nil
	}
	tSeparate := time.Since(tSepStart)

	// Batch-read all non-zero blocks into a temporary buffer.
	tAllocStart := time.Now()
	batchLBAs := make([]uint32, len(nonZero))
	for i, bp := range nonZero {
		batchLBAs[i] = bp.blockNum
	}
	batchBuf := make([]byte, len(nonZero)*blockSize)
	tAlloc := time.Since(tAllocStart)

	tDMAStart := time.Now()
	if err := f.fs.readBlocks(batchLBAs, batchBuf); err != nil {
		return 0, err
	}
	tDMA := time.Since(tDMAStart)

	tCopyStart := time.Now()
	// Copy from batch buffer into the correct positions in dst.
	for i, bp := range nonZero {
		srcOff := i * blockSize
		dstOff := bp.dstIndex * blockSize
		end := dstOff + blockSize
		if end > fileSize {
			end = fileSize
		}
		copy(dst[dstOff:end], batchBuf[srcOff:srcOff+(end-dstOff)])
	}
	tCopy := time.Since(tCopyStart)

	if h := ReadIntoTimingHook; h != nil {
		h(ReadIntoTimings{
			Resolve:  tResolve,
			Separate: tSeparate,
			Alloc:    tAlloc,
			DMA:      tDMA,
			Copy:     tCopy,
			Total:    time.Since(t0),
			Blocks:   len(nonZero),
		})
	}

	return fileSize, nil
}

// Write writes data at the current position, extending the file if needed.
func (f *File) Write(data []byte) (int, error) {
	if !f.fs.writable {
		return 0, ErrReadOnly
	}
	if f.inode.IsDir() {
		return 0, ErrNotFile
	}

	blockSize := uint64(f.fs.blockSize)
	if f.blockBuf == nil {
		f.blockBuf = make([]byte, blockSize)
		f.blockIdx = -1
	}

	totalWritten := 0
	for len(data) > 0 {
		blkIdx := int64(f.pos / blockSize)
		blkOff := f.pos % blockSize

		// Allocate a new block if we're beyond current size
		if f.pos >= uint64(f.inode.Size) {
			// Check if this block already exists (could be a partial block extension)
			existing, _ := f.fs.inodeBlockNum(&f.inode, uint32(blkIdx))
			if existing == 0 {
				newBlock, err := f.fs.allocBlock()
				if err != nil {
					return totalWritten, err
				}
				if err := f.fs.setInodeBlock(&f.inode, uint32(blkIdx), newBlock); err != nil {
					return totalWritten, err
				}
				f.inode.Blocks += f.fs.blockSize / 512
				// Zero the new block
				if err := f.fs.writeBlock(newBlock, make([]byte, f.fs.blockSize)); err != nil {
					return totalWritten, err
				}
			}
		}

		// Load current block
		if blkIdx != f.blockIdx {
			blockNum, err := f.fs.inodeBlockNum(&f.inode, uint32(blkIdx))
			if err != nil {
				return totalWritten, err
			}
			if blockNum == 0 {
				for i := range f.blockBuf {
					f.blockBuf[i] = 0
				}
			} else {
				if err := f.fs.readBlock(blockNum, f.blockBuf); err != nil {
					return totalWritten, err
				}
			}
			f.blockIdx = blkIdx
		}

		// How much to write in this block?
		remaining := blockSize - blkOff
		toWrite := uint64(len(data))
		if toWrite > remaining {
			toWrite = remaining
		}

		copy(f.blockBuf[blkOff:blkOff+toWrite], data[:toWrite])

		// Write block back
		blockNum, err := f.fs.inodeBlockNum(&f.inode, uint32(blkIdx))
		if err != nil {
			return totalWritten, err
		}
		if err := f.fs.writeBlock(blockNum, f.blockBuf); err != nil {
			return totalWritten, err
		}

		data = data[toWrite:]
		f.pos += toWrite
		totalWritten += int(toWrite)

		// Extend file size if needed
		if f.pos > uint64(f.inode.Size) {
			f.inode.Size = uint32(f.pos)
		}
	}

	// Write updated inode
	if err := f.fs.WriteInode(f.inum, &f.inode); err != nil {
		return totalWritten, err
	}

	return totalWritten, nil
}

// Seek moves the file position to offset bytes from the start.
// Seeking past EOF is allowed (consistent with lseek behavior); a subsequent
// read will return 0 bytes, and a subsequent write will extend the file.
func (f *File) Seek(offset uint64) error {
	f.pos = offset
	return nil
}

// Size returns the file size in bytes.
func (f *File) Size() uint32 {
	return f.inode.Size
}

// Name returns the filename.
func (f *File) Name() string {
	return f.name
}

// Inode returns the inode number.
func (f *File) InodeNum() uint32 {
	return f.inum
}

// InodeRaw returns a pointer to the file's inode for block list resolution.
func (f *File) InodeRaw() *Inode {
	return &f.inode
}

// Close releases resources.
func (f *File) Close() error {
	f.blockBuf = nil
	f.blockIdx = -1
	return nil
}
