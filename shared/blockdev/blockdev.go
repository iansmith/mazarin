// Package blockdev defines block device interfaces for filesystem implementations.
// This is a minimal interface without hardware-specific dependencies.
package blockdev

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
