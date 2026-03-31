package blockdev

import "fmt"

// MemBlockDevice is a memory-backed block device for ramdisks.
// It implements BlockDevice with a simple []byte backing store.
type MemBlockDevice struct {
	name      string
	data      []byte
	blockSize uint64
	numBlocks uint64
}

// NewMemBlockDevice creates a memory-backed block device.
// The backing store is allocated as blockSize * numBlocks bytes, zeroed.
func NewMemBlockDevice(name string, blockSize, numBlocks uint64) *MemBlockDevice {
	return &MemBlockDevice{
		name:      name,
		data:      make([]byte, blockSize*numBlocks),
		blockSize: blockSize,
		numBlocks: numBlocks,
	}
}

// NewMemBlockDeviceFromBacking creates a memory-backed block device using
// a caller-provided backing slice. The slice must be at least
// blockSize * numBlocks bytes. This allows the backing store to live
// outside the Go heap (e.g., kernel-allocated pages via AllocPagesSlice),
// avoiding GC pressure from large ramdisks.
func NewMemBlockDeviceFromBacking(name string, blockSize uint64, backing []byte) *MemBlockDevice {
	numBlocks := uint64(len(backing)) / blockSize
	return &MemBlockDevice{
		name:      name,
		data:      backing,
		blockSize: blockSize,
		numBlocks: numBlocks,
	}
}

func (m *MemBlockDevice) Name() string      { return m.name }
func (m *MemBlockDevice) Close() error      { return nil }
func (m *MemBlockDevice) BlockSize() uint64  { return m.blockSize }
func (m *MemBlockDevice) NumBlocks() uint64  { return m.numBlocks }

func (m *MemBlockDevice) ReadBlock(lba uint64, buf []byte) error {
	if lba >= m.numBlocks {
		return fmt.Errorf("memblockdev %s: read lba %d out of range (max %d)", m.name, lba, m.numBlocks-1)
	}
	off := lba * m.blockSize
	copy(buf, m.data[off:off+m.blockSize])
	return nil
}

// ReadBlocks reads multiple blocks in a single operation.
// For MemBlockDevice this is a simple loop, but satisfying the
// BatchBlockDevice interface allows ext2 to use the batch path.
func (m *MemBlockDevice) ReadBlocks(lbas []uint64, dst []byte) error {
	bs := m.blockSize
	for i, lba := range lbas {
		if lba >= m.numBlocks {
			return fmt.Errorf("memblockdev %s: read lba %d out of range (max %d)", m.name, lba, m.numBlocks-1)
		}
		srcOff := lba * bs
		dstOff := uint64(i) * bs
		copy(dst[dstOff:dstOff+bs], m.data[srcOff:srcOff+bs])
	}
	return nil
}

func (m *MemBlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if lba >= m.numBlocks {
		return fmt.Errorf("memblockdev %s: write lba %d out of range (max %d)", m.name, lba, m.numBlocks-1)
	}
	off := lba * m.blockSize
	copy(m.data[off:off+m.blockSize], buf[:m.blockSize])
	return nil
}
