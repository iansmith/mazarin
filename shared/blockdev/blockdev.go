// Package blockdev defines block device interfaces for filesystem implementations.
// This is a minimal interface without hardware-specific dependencies.
package blockdev

// Error codes for BlockDeviceRaw methods (allocation-free error reporting)
const (
	ErrCodeSuccess       = 0 // No error
	ErrCodeIO            = 1 // I/O error
	ErrCodeInvalidLBA    = 2 // LBA out of range
	ErrCodeBufferTooSmall = 3 // Buffer smaller than block size
	ErrCodeDeviceClosed  = 4 // Device is closed
	ErrCodeTimeout       = 5 // Operation timed out
)

// BlockDevice provides block-level access to storage devices.
// Implementations include UEFI Block I/O, VirtIO block, etc.
type BlockDevice interface {
	// Name returns a human-readable device name for debugging.
	Name() string

	// Close shuts down the device and releases resources.
	Close() error

	// ReadBlock reads a single block at the given LBA.
	// buf must be at least BlockSize() bytes.
	ReadBlock(lba uint64, buf []byte) error

	// WriteBlock writes a single block at the given LBA.
	// buf must be exactly BlockSize() bytes.
	WriteBlock(lba uint64, buf []byte) error

	// BlockSize returns the size of a single block in bytes.
	// Typically 512 or 4096.
	BlockSize() uint64

	// NumBlocks returns the total number of blocks on the device.
	NumBlocks() uint64
}

// BatchBlockDevice extends BlockDevice with multi-block read support.
// Implementations that can read multiple blocks more efficiently than
// N individual ReadBlock calls (e.g., DMA batching) should implement
// this interface. The ext2 filesystem detects this interface and uses
// it for directory reads, file reads, and bitmap loading.
type BatchBlockDevice interface {
	BlockDevice

	// ReadBlocks reads multiple blocks in a single operation.
	// lbas contains the LBA for each block to read (may be non-contiguous).
	// dst receives the data: block i is written at dst[i*BlockSize():(i+1)*BlockSize()].
	// dst must be at least len(lbas)*BlockSize() bytes.
	ReadBlocks(lbas []uint64, dst []byte) error
}

// BlockDeviceRaw extends BlockDevice with allocation-free raw methods.
// These methods return error codes instead of error interfaces, allowing
// early boot code (before heap initialization) to perform I/O without allocation.
type BlockDeviceRaw interface {
	BlockDevice // Embed normal interface

	// ReadBlockRaw reads a single block at the given LBA without allocating.
	// Returns (bytes_read, error_code) where error_code is 0 on success.
	ReadBlockRaw(lba uint64, buf []byte) (n int, errCode int)

	// WriteBlockRaw writes a single block at the given LBA without allocating.
	// Returns (bytes_written, error_code) where error_code is 0 on success.
	WriteBlockRaw(lba uint64, buf []byte) (n int, errCode int)
}
