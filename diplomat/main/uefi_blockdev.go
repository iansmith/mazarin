// diplomat/main/uefi_blockdev.go - UEFI Block I/O wrapper for FAT32 filesystem
//
// Implements shared/blockdev.BlockDevice using UEFI's EFI_BLOCK_IO_PROTOCOL.
// This allows diplomat to use the shared FAT32 implementation.

package main

import (
	"mazzy/shared/blockdev"
	"unsafe"
)

// EFI_BLOCK_IO_PROTOCOL offsets (for x86_64)
// See UEFI Spec 13.17 Block I/O Protocol
const (
	BlockIORevision       = 0   // UINT64 Revision
	BlockIOMedia          = 8   // EFI_BLOCK_IO_MEDIA *Media
	BlockIOReset          = 16  // EFI_BLOCK_IO_RESET Reset
	BlockIOReadBlocks     = 24  // EFI_BLOCK_IO_READ_BLOCKS ReadBlocks
	BlockIOWriteBlocks    = 32  // EFI_BLOCK_IO_WRITE_BLOCKS WriteBlocks
	BlockIOFlushBlocks    = 40  // EFI_BLOCK_IO_FLUSH_BLOCKS FlushBlocks
)

// EFI_BLOCK_IO_MEDIA offsets
const (
	MediaMediaId          = 0   // UINT32 MediaId
	MediaRemovableMedia   = 4   // BOOLEAN RemovableMedia
	MediaMediaPresent     = 5   // BOOLEAN MediaPresent
	MediaLogicalPartition = 6   // BOOLEAN LogicalPartition
	MediaReadOnly         = 7   // BOOLEAN ReadOnly
	MediaWriteCaching     = 8   // BOOLEAN WriteCaching
	MediaBlockSize        = 12  // UINT32 BlockSize
	MediaIoAlign          = 16  // UINT32 IoAlign
	MediaLastBlock        = 24  // EFI_LBA LastBlock (UINT64)
	// Extended fields in newer revisions omitted
)

// UEFIBlockDevice wraps UEFI EFI_BLOCK_IO_PROTOCOL as blockdev.BlockDevice
type UEFIBlockDevice struct {
	protocol  uintptr // Pointer to EFI_BLOCK_IO_PROTOCOL
	media     uintptr // Pointer to EFI_BLOCK_IO_MEDIA
	blockSize uint64
	numBlocks uint64
	mediaId   uint32
}

// globalBlockDev avoids heap allocation in UEFI environment
var globalBlockDev UEFIBlockDevice

// Ensure UEFIBlockDevice implements blockdev.BlockDevice
var _ blockdev.BlockDevice = (*UEFIBlockDevice)(nil)

// NewUEFIBlockDevice creates a new block device wrapper from a UEFI Block I/O protocol
func NewUEFIBlockDevice(protocol uintptr) (*UEFIBlockDevice, error) {
	if protocol == 0 {
		return nil, &blockDevError{"nil protocol pointer"}
	}

	// Read Media pointer from protocol
	media := *(*uintptr)(unsafe.Pointer(protocol + BlockIOMedia))
	if media == 0 {
		return nil, &blockDevError{"nil media pointer"}
	}

	// Read block size and last block from media
	blockSize := uint64(*(*uint32)(unsafe.Pointer(media + MediaBlockSize)))
	lastBlock := *(*uint64)(unsafe.Pointer(media + MediaLastBlock))
	mediaId := *(*uint32)(unsafe.Pointer(media + MediaMediaId))

	globalBlockDev.protocol = protocol
	globalBlockDev.media = media
	globalBlockDev.blockSize = blockSize
	globalBlockDev.numBlocks = lastBlock + 1 // LastBlock is 0-indexed
	globalBlockDev.mediaId = mediaId
	return &globalBlockDev, nil
}

// Name returns the device name
func (d *UEFIBlockDevice) Name() string {
	return "uefi-block"
}

// Close releases resources (no-op for UEFI)
func (d *UEFIBlockDevice) Close() error {
	return nil
}

// ReadBlock reads a single block at the given LBA
func (d *UEFIBlockDevice) ReadBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) < d.blockSize {
		return &blockDevError{"buffer too small"}
	}

	// Get ReadBlocks function pointer
	readBlocks := *(*uintptr)(unsafe.Pointer(d.protocol + BlockIOReadBlocks))
	if readBlocks == 0 {
		return &blockDevError{"ReadBlocks not available"}
	}

	// Call ReadBlocks(This, MediaId, LBA, BufferSize, Buffer)
	status := uefiCallBlockIORead(
		d.protocol,
		d.mediaId,
		lba,
		d.blockSize,
		uintptr(unsafe.Pointer(&buf[0])),
		readBlocks,
	)

	if status != 0 {
		return &blockDevError{"ReadBlocks failed"}
	}
	return nil
}

// WriteBlock writes a single block at the given LBA
func (d *UEFIBlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) < d.blockSize {
		return &blockDevError{"buffer too small"}
	}

	// Get WriteBlocks function pointer
	writeBlocks := *(*uintptr)(unsafe.Pointer(d.protocol + BlockIOWriteBlocks))
	if writeBlocks == 0 {
		return &blockDevError{"WriteBlocks not available"}
	}

	// Call WriteBlocks(This, MediaId, LBA, BufferSize, Buffer)
	status := uefiCallBlockIOWrite(
		d.protocol,
		d.mediaId,
		lba,
		d.blockSize,
		uintptr(unsafe.Pointer(&buf[0])),
		writeBlocks,
	)

	if status != 0 {
		return &blockDevError{"WriteBlocks failed"}
	}
	return nil
}

// BlockSize returns the block size in bytes
func (d *UEFIBlockDevice) BlockSize() uint64 {
	return d.blockSize
}

// NumBlocks returns the total number of blocks
func (d *UEFIBlockDevice) NumBlocks() uint64 {
	return d.numBlocks
}

// blockDevError represents a block device error
type blockDevError struct {
	msg string
}

func (e *blockDevError) Error() string {
	return "blockdev: " + e.msg
}

// uefiCallBlockIORead is implemented in assembly (uefi_calls_amd64.s)
// Calls EFI_BLOCK_IO_PROTOCOL.ReadBlocks using MS x64 ABI
func uefiCallBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS

// uefiCallBlockIOWrite is implemented in assembly (uefi_calls_amd64.s)
// Calls EFI_BLOCK_IO_PROTOCOL.WriteBlocks using MS x64 ABI
func uefiCallBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS
