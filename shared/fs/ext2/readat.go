package ext2

// ReadAt reads len(p) bytes from the file at byte offset off,
// io.ReaderAt-style: it neither consults nor advances the file position.
// Returns the number of bytes read; a read starting at or past EOF returns
// (0, ErrEndOfFile), and a read that runs into EOF returns the bytes read
// with a nil error.
//
// MAZ-165 Phase 0 stub: delegates to the per-block slow path (Seek + Read),
// which costs 3 uncached device reads per 4 KB block in the double-indirect
// range and never issues a batched ReadBlocks. The round-trip-counting red
// test therefore reaches its assertion and fails it (48 single / 0 batched
// for a 64 KB window vs the required ≤ 3 with ≥ 1 batched). The real
// implementation resolves the window's block list with the L2 tables fetched
// in one batched read, then reads all data blocks in a second batched read.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	savedPos := f.pos
	defer func() { f.pos = savedPos }()

	if err := f.Seek(uint64(off)); err != nil {
		return 0, err
	}
	return f.Read(p)
}
