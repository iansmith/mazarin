// Package fat32 provides a read-only FAT32 filesystem implementation.
// It supports both heap-based allocation (for normal Go programs) and
// callback-based allocation (for bootloaders without full Go runtime).
package fat32

import (
	"mazzy/shared/blockdev"
)

// debugOut writes a byte to QEMU debug port 0xE9 for debugging
// Implemented in debug_amd64.s (x86) or stubbed for other platforms
func debugOut(c byte)

// FAT32 special cluster values
const (
	FAT32ClusterFree     = 0x00000000
	FAT32ClusterReserved = 0x00000001
	FAT32ClusterBad      = 0x0FFFFFF7
	FAT32ClusterEOF      = 0x0FFFFFF8 // >= this value means end of chain
)

// FAT32Error represents a FAT32-specific error
type FAT32Error struct {
	msg string
}

func (e *FAT32Error) Error() string {
	return "fat32: " + e.msg
}

// Common errors - these are pre-allocated to avoid allocation on error paths
var (
	ErrNotFAT32       = &FAT32Error{"not a FAT32 filesystem"}
	ErrInvalidBPB     = &FAT32Error{"invalid BPB"}
	ErrReadFailed     = &FAT32Error{"read failed"}
	ErrNotFound       = &FAT32Error{"file not found"}
	ErrNotFile        = &FAT32Error{"not a file"}
	ErrInvalidPath    = &FAT32Error{"invalid path"}
	ErrEndOfFile      = &FAT32Error{"end of file"}
	ErrBadCluster     = &FAT32Error{"bad cluster"}
	ErrInvalidSeek    = &FAT32Error{"invalid seek position"}
	ErrAllocFailed    = &FAT32Error{"allocation failed"}
	ErrClusterTooLarge = &FAT32Error{"cluster size exceeds buffer"}
)

// FileSystem represents a mounted FAT32 filesystem.
// Use pointer receivers exclusively to avoid copies.
type FileSystem struct {
	device       blockdev.BlockDevice
	alloc        Allocator // allocation callback (nil = panic on alloc attempt)
	bytesPerSec  uint16
	secPerClus   uint8
	rsvdSecCnt   uint16
	numFATs      uint8
	fatSize32    uint32
	rootCluster  uint32
	fatStartSec  uint64
	dataStartSec uint64
	bytesPerClus uint32
	totalSectors uint32
	// Sector buffer for FAT reads (embedded to avoid allocation)
	fatBuffer [512]byte
}

// MountWith mounts a FAT32 filesystem using the provided allocator.
// The allocator is used for the FileSystem struct itself and any
// subsequent allocations (buffers, directory entries, etc.).
//
// For environments without Go heap (diplomat, cardinal), pass a
// bump allocator or similar. For normal Go programs, use Mount().
func MountWith(device blockdev.BlockDevice, alloc Allocator) (*FileSystem, error) {
	if alloc == nil {
		return nil, &FAT32Error{"allocator cannot be nil"}
	}

	// Allocate FileSystem using the provided allocator
	fs := allocType[FileSystem](alloc)
	if fs == nil {
		return nil, ErrAllocFailed
	}

	fs.alloc = alloc
	if err := fs.init(device); err != nil {
		return nil, err
	}
	return fs, nil
}

// Mount mounts a FAT32 filesystem using the Go heap allocator.
// This is the standard API for normal Go programs with full runtime.
func Mount(device blockdev.BlockDevice) (*FileSystem, error) {
	return MountWith(device, DefaultAllocator())
}

// MountInto initializes a pre-allocated FileSystem struct.
// This is the lowest-level API for environments where even the
// allocator callback overhead is undesirable.
// The caller must zero the FileSystem before calling.
func MountInto(fs *FileSystem, device blockdev.BlockDevice, alloc Allocator) error {
	fs.alloc = alloc
	return fs.init(device)
}

// init performs the actual filesystem initialization
func (fs *FileSystem) init(device blockdev.BlockDevice) error {
	debugOut('1') // entered init
	if device.BlockSize() != 512 {
		return &FAT32Error{"only 512-byte sectors supported"}
	}
	debugOut('2') // after BlockSize check

	fs.device = device
	debugOut('3') // after device assignment

	// Read boot sector into fs.fatBuffer
	if err := device.ReadBlock(0, fs.fatBuffer[:]); err != nil {
		return ErrReadFailed
	}
	debugOut('4') // after ReadBlock

	// Check boot signature
	if fs.fatBuffer[510] != 0x55 || fs.fatBuffer[511] != 0xAA {
		return ErrNotFAT32
	}

	// Parse BPB (BIOS Parameter Block)
	fs.bytesPerSec = uint16(fs.fatBuffer[11]) | (uint16(fs.fatBuffer[12]) << 8)
	fs.secPerClus = fs.fatBuffer[13]
	fs.rsvdSecCnt = uint16(fs.fatBuffer[14]) | (uint16(fs.fatBuffer[15]) << 8)
	fs.numFATs = fs.fatBuffer[16]

	// Check for FAT32 (rootEntCnt must be 0 for FAT32)
	rootEntCnt := uint16(fs.fatBuffer[17]) | (uint16(fs.fatBuffer[18]) << 8)
	if rootEntCnt != 0 {
		return ErrNotFAT32
	}

	// Total sectors (16-bit or 32-bit)
	totSec16 := uint16(fs.fatBuffer[19]) | (uint16(fs.fatBuffer[20]) << 8)
	if totSec16 != 0 {
		fs.totalSectors = uint32(totSec16)
	} else {
		fs.totalSectors = uint32(fs.fatBuffer[32]) |
			(uint32(fs.fatBuffer[33]) << 8) |
			(uint32(fs.fatBuffer[34]) << 16) |
			(uint32(fs.fatBuffer[35]) << 24)
	}

	// FAT32 specific fields (offset 36+)
	fs.fatSize32 = uint32(fs.fatBuffer[36]) |
		(uint32(fs.fatBuffer[37]) << 8) |
		(uint32(fs.fatBuffer[38]) << 16) |
		(uint32(fs.fatBuffer[39]) << 24)

	fs.rootCluster = uint32(fs.fatBuffer[44]) |
		(uint32(fs.fatBuffer[45]) << 8) |
		(uint32(fs.fatBuffer[46]) << 16) |
		(uint32(fs.fatBuffer[47]) << 24)

	// Validate
	if fs.bytesPerSec != 512 {
		return ErrInvalidBPB
	}
	if fs.secPerClus == 0 || (fs.secPerClus&(fs.secPerClus-1)) != 0 {
		return ErrInvalidBPB // Must be power of 2
	}
	if fs.numFATs == 0 {
		return ErrInvalidBPB
	}
	if fs.fatSize32 == 0 {
		return ErrNotFAT32
	}

	// Calculate derived values
	fs.fatStartSec = uint64(fs.rsvdSecCnt)
	fs.dataStartSec = fs.fatStartSec + uint64(fs.numFATs)*uint64(fs.fatSize32)
	fs.bytesPerClus = uint32(fs.secPerClus) * uint32(fs.bytesPerSec)

	debugOut('5') // init complete
	return nil
}

// Alloc allocates memory using the filesystem's allocator.
// Returns nil if allocation fails or no allocator is set.
func (fs *FileSystem) Alloc(size uintptr) []byte {
	if fs.alloc == nil {
		return nil
	}
	return allocSlice[byte](fs.alloc, int(size))
}

// RootCluster returns the root directory cluster
func (fs *FileSystem) RootCluster() uint32 {
	return fs.rootCluster
}

// BytesPerCluster returns the cluster size in bytes
func (fs *FileSystem) BytesPerCluster() uint32 {
	return fs.bytesPerClus
}

// SectorsPerCluster returns the number of sectors per cluster
func (fs *FileSystem) SectorsPerCluster() uint8 {
	return fs.secPerClus
}

// ClusterToSector converts a cluster number to the first sector of that cluster
func (fs *FileSystem) ClusterToSector(cluster uint32) uint64 {
	// Clusters 0 and 1 are reserved
	return fs.dataStartSec + uint64(cluster-2)*uint64(fs.secPerClus)
}

// Device returns the underlying block device
func (fs *FileSystem) Device() blockdev.BlockDevice {
	return fs.device
}

// FATStartSector returns the starting sector of the FAT
func (fs *FileSystem) FATStartSector() uint64 {
	return fs.fatStartSec
}

// ReadCluster reads an entire cluster into the buffer.
// The buffer must be at least BytesPerCluster() bytes.
func (fs *FileSystem) ReadCluster(cluster uint32, buf []byte) error {
	if uint32(len(buf)) < fs.bytesPerClus {
		return ErrClusterTooLarge
	}

	startSector := fs.ClusterToSector(cluster)
	for i := uint8(0); i < fs.secPerClus; i++ {
		offset := uint32(i) * 512
		if err := fs.device.ReadBlock(startSector+uint64(i), buf[offset:offset+512]); err != nil {
			return ErrReadFailed
		}
	}
	return nil
}

// ReadFATEntry reads the FAT entry for the given cluster.
// Returns the next cluster in the chain, or a special value (EOF, bad, etc).
func (fs *FileSystem) ReadFATEntry(cluster uint32) (uint32, error) {
	// Each FAT32 entry is 4 bytes
	fatOffset := cluster * 4
	fatSector := fs.fatStartSec + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	// Read the FAT sector into our buffer
	if err := fs.device.ReadBlock(fatSector, fs.fatBuffer[:]); err != nil {
		return 0, ErrReadFailed
	}

	// Read 4-byte entry (little-endian), mask off high 4 bits
	entry := uint32(fs.fatBuffer[fatEntryOffset]) |
		(uint32(fs.fatBuffer[fatEntryOffset+1]) << 8) |
		(uint32(fs.fatBuffer[fatEntryOffset+2]) << 16) |
		(uint32(fs.fatBuffer[fatEntryOffset+3]) << 24)
	entry &= 0x0FFFFFFF // FAT32 uses only 28 bits

	return entry, nil
}

// IsEOF checks if a FAT entry indicates end of chain
func IsEOF(entry uint32) bool {
	return entry >= FAT32ClusterEOF
}

// IsBad checks if a FAT entry indicates a bad cluster
func IsBad(entry uint32) bool {
	return entry == FAT32ClusterBad
}
