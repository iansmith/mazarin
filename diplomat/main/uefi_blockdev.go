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
		return nil, &errNilProtocol
	}

	// Read Media pointer from protocol
	media := *(*uintptr)(unsafe.Pointer(protocol + BlockIOMedia))
	if media == 0 {
		return nil, &errNilMedia
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
		return &errBufferTooSmall
	}

	// Get ReadBlocks function pointer
	readBlocks := *(*uintptr)(unsafe.Pointer(d.protocol + BlockIOReadBlocks))
	if readBlocks == 0 {
		return &errReadBlocksNotAvailable
	}

	// Call ReadBlocks(This, MediaId, LBA, BufferSize, Buffer)
	status := plat.BlockIORead(
		d.protocol,
		d.mediaId,
		lba,
		d.blockSize,
		uintptr(unsafe.Pointer(&buf[0])),
		readBlocks,
	)

	if status != 0 {
		return &errReadBlocksFailed
	}
	return nil
}

// WriteBlock writes a single block at the given LBA
func (d *UEFIBlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) < d.blockSize {
		return &errBufferTooSmall
	}

	// Get WriteBlocks function pointer
	writeBlocks := *(*uintptr)(unsafe.Pointer(d.protocol + BlockIOWriteBlocks))
	if writeBlocks == 0 {
		return &errWriteBlocksNotAvailable
	}

	// Call WriteBlocks(This, MediaId, LBA, BufferSize, Buffer)
	status := plat.BlockIOWrite(
		d.protocol,
		d.mediaId,
		lba,
		d.blockSize,
		uintptr(unsafe.Pointer(&buf[0])),
		writeBlocks,
	)

	if status != 0 {
		return &errWriteBlocksFailed
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

// blockDevError represents a block device error.
// All instances MUST be pre-allocated as package-level variables
// to avoid Go heap allocation (diplomat has no Go runtime heap).
type blockDevError struct {
	msg string
}

func (e *blockDevError) Error() string {
	return e.msg
}

// Pre-allocated errors for uefi_blockdev.go
var (
	errNilProtocol             = blockDevError{"blockdev: nil protocol pointer"}
	errNilMedia                = blockDevError{"blockdev: nil media pointer"}
	errBufferTooSmall          = blockDevError{"blockdev: buffer too small"}
	errReadBlocksNotAvailable  = blockDevError{"blockdev: ReadBlocks not available"}
	errReadBlocksFailed        = blockDevError{"blockdev: ReadBlocks failed"}
	errWriteBlocksNotAvailable = blockDevError{"blockdev: WriteBlocks not available"}
	errWriteBlocksFailed       = blockDevError{"blockdev: WriteBlocks failed"}
)

// Pre-allocated errors for uefi_protocol.go
var (
	errBootServicesNotAvailable = blockDevError{"blockdev: boot services not available"}
	errLoadedImageProtocol      = blockDevError{"blockdev: failed to get LoadedImage protocol"}
	errNoDeviceHandle           = blockDevError{"blockdev: no device handle in LoadedImage"}
	errBlockIOProtocol          = blockDevError{"blockdev: failed to get BlockIO protocol"}
)

// Pre-allocated errors for elf_loader.go
var (
	errFailedReadELFHeader      = blockDevError{"blockdev: failed to read ELF header"}
	errNotAnELF                 = blockDevError{"blockdev: not an ELF file"}
	errNotELF64LE               = blockDevError{"blockdev: not ELF64 little-endian"}
	errMachineTypeMismatch      = blockDevError{"blockdev: ELF machine type mismatch"}
	errTooManyProgramHeaders    = blockDevError{"blockdev: too many program headers"}
	errFailedReadProgramHeaders = blockDevError{"blockdev: failed to read program headers"}
	errNoLOADSegments           = blockDevError{"blockdev: no LOAD segments"}
	errAllocationFailed         = blockDevError{"blockdev: allocation failed"}
	errEFINotDir                = blockDevError{"blockdev: EFI is not a directory"}
	errLinuxNotDir              = blockDevError{"blockdev: Linux is not a directory"}
	errDNewSimpleFileFailed     = blockDevError{"blockdev: dNew SimpleFile failed"}
	errFileNotFound             = blockDevError{"blockdev: file not found"}
)

// Pre-allocated errors for fat32_walk.go
var (
	errClusterTooLarge    = blockDevError{"blockdev: cluster size too large"}
	errInvalidClusterChain = blockDevError{"blockdev: invalid cluster chain"}
)

// Pre-allocated errors for pagetable_arm64.go / pagetable_amd64.go
var (
	errKernelMappingSpans     = blockDevError{"blockdev: kernel mapping spans multiple entries"}
	errFailedAllocPageTableSet = blockDevError{"blockdev: failed to allocate PageTableSet"}
	errAllocatePagesFailed    = blockDevError{"blockdev: AllocatePages failed"}
)

// uefiCallBlockIORead is implemented in assembly (uefi_calls_amd64.s)
// Calls EFI_BLOCK_IO_PROTOCOL.ReadBlocks using MS x64 ABI
func uefiCallBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS

// uefiCallBlockIOWrite is implemented in assembly (uefi_calls_amd64.s)
// Calls EFI_BLOCK_IO_PROTOCOL.WriteBlocks using MS x64 ABI
func uefiCallBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS
