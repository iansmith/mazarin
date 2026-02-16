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

// Error codes for *Raw methods (allocation-free error reporting)
const (
	ErrCodeSuccess         = 0  // No error
	ErrCodeNotFAT32        = 1  // Not a FAT32 filesystem
	ErrCodeInvalidBPB      = 2  // Invalid BPB
	ErrCodeReadFailed      = 3  // Read failed
	ErrCodeNotFound        = 4  // File not found
	ErrCodeNotFile         = 5  // Not a file
	ErrCodeInvalidPath     = 6  // Invalid path
	ErrCodeEndOfFile       = 7  // End of file
	ErrCodeBadCluster      = 8  // Bad cluster
	ErrCodeInvalidSeek     = 9  // Invalid seek position
	ErrCodeAllocFailed     = 10 // Allocation failed
	ErrCodeClusterTooLarge = 11 // Cluster size exceeds buffer
)

// errorFromCode converts an error code to an error interface.
func errorFromCode(code int) error {
	switch code {
	case ErrCodeSuccess:
		return nil
	case ErrCodeNotFAT32:
		return ErrNotFAT32
	case ErrCodeInvalidBPB:
		return ErrInvalidBPB
	case ErrCodeReadFailed:
		return ErrReadFailed
	case ErrCodeNotFound:
		return ErrNotFound
	case ErrCodeNotFile:
		return ErrNotFile
	case ErrCodeInvalidPath:
		return ErrInvalidPath
	case ErrCodeEndOfFile:
		return ErrEndOfFile
	case ErrCodeBadCluster:
		return ErrBadCluster
	case ErrCodeInvalidSeek:
		return ErrInvalidSeek
	case ErrCodeAllocFailed:
		return ErrAllocFailed
	case ErrCodeClusterTooLarge:
		return ErrClusterTooLarge
	default:
		return ErrReadFailed // Generic fallback
	}
}

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
	debugOut('A') // entered MountWith
	if alloc == nil {
		return nil, &FAT32Error{"allocator cannot be nil"}
	}
	debugOut('B') // alloc not nil

	// Allocate FileSystem using the provided allocator
	debugOut('C') // before allocType
	fs := allocType[FileSystem](alloc)
	debugOut('D') // after allocType
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
// Uses fs.device.ReadBlock() directly — no type assertions that would
// require heap allocation (diplomat doesn't initialize the Go heap).
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

// ReadClusterRaw reads a complete cluster without allocating error interfaces.
// Returns (bytes_read, error_code) where error_code is 0 on success.
func (fs *FileSystem) ReadClusterRaw(cluster uint32, buf []byte) (int, int) {
	if uint32(len(buf)) < fs.bytesPerClus {
		return 0, ErrCodeClusterTooLarge
	}

	startSector := fs.ClusterToSector(cluster)
	totalRead := 0

	for i := uint8(0); i < fs.secPerClus; i++ {
		offset := uint32(i) * 512
		var n, errCode int

		if raw, ok := fs.device.(blockdev.BlockDeviceRaw); ok {
			n, errCode = raw.ReadBlockRaw(startSector+uint64(i), buf[offset:offset+512])
		} else {
			if err := fs.device.ReadBlock(startSector+uint64(i), buf[offset:offset+512]); err != nil {
				errCode = ErrCodeReadFailed
			} else {
				n = 512
			}
		}

		if errCode != 0 {
			return totalRead, ErrCodeReadFailed
		}
		totalRead += n
	}
	return totalRead, ErrCodeSuccess
}

// ReadFATEntry reads the FAT entry for the given cluster.
// Returns the next cluster in the chain, or a special value (EOF, bad, etc).
// Uses fs.device.ReadBlock() directly — no type assertions that would
// require heap allocation (diplomat doesn't initialize the Go heap).
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

// ReadFATEntryRaw reads a FAT entry without allocating error interfaces.
// Returns (entry_value, error_code) where error_code is 0 on success.
func (fs *FileSystem) ReadFATEntryRaw(cluster uint32) (uint32, int) {
	// Each FAT32 entry is 4 bytes
	fatOffset := cluster * 4
	fatSector := fs.fatStartSec + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	// Read the FAT sector into our buffer
	var errCode int
	if raw, ok := fs.device.(blockdev.BlockDeviceRaw); ok {
		_, errCode = raw.ReadBlockRaw(fatSector, fs.fatBuffer[:])
	} else {
		if err := fs.device.ReadBlock(fatSector, fs.fatBuffer[:]); err != nil {
			errCode = ErrCodeReadFailed
		}
	}
	if errCode != 0 {
		return 0, ErrCodeReadFailed
	}

	// Read 4-byte entry (little-endian), mask off high 4 bits
	entry := uint32(fs.fatBuffer[fatEntryOffset]) |
		(uint32(fs.fatBuffer[fatEntryOffset+1]) << 8) |
		(uint32(fs.fatBuffer[fatEntryOffset+2]) << 16) |
		(uint32(fs.fatBuffer[fatEntryOffset+3]) << 24)
	entry &= 0x0FFFFFFF // FAT32 uses only 28 bits

	return entry, ErrCodeSuccess
}

// IsEOF checks if a FAT entry indicates end of chain
func IsEOF(entry uint32) bool {
	return entry >= FAT32ClusterEOF
}

// IsBad checks if a FAT entry indicates a bad cluster
func IsBad(entry uint32) bool {
	return entry == FAT32ClusterBad
}

// ============================================================================
// RISC-V Early Boot Helpers (no error interface returns)
// ============================================================================

// SetAlloc sets the allocator for the filesystem.
func (fs *FileSystem) SetAlloc(alloc Allocator) {
	fs.alloc = alloc
}

// SetDevice sets the block device for the filesystem.
func (fs *FileSystem) SetDevice(dev blockdev.BlockDevice) {
	fs.device = dev
}

// FATBuffer returns a pointer to the internal FAT buffer.
// Used for RISC-V early boot to avoid method calls.
func (fs *FileSystem) FATBuffer() *[512]byte {
	return &fs.fatBuffer
}

// CopyBootSector copies a boot sector buffer into the FileSystem's internal buffer.
// Used for RISC-V early boot to avoid method calls during ReadBlock.
func CopyBootSector(fs *FileSystem, buf *[512]byte) {
	copy(fs.fatBuffer[:], buf[:])
}

// ReadBootSector reads the boot sector into the internal buffer.
// Returns false on error (use for early boot to avoid error interfaces).
func (fs *FileSystem) ReadBootSector() bool {
	debugOut('B') // ReadBootSector entered
	if fs.device == nil {
		return false
	}

	debugOut('C') // about to read block

	// Try to read boot sector - use blank identifier to avoid capturing error
	// This MIGHT still trigger allocation, but let's try it
	_ = fs.device.ReadBlock(0, fs.fatBuffer[:])

	debugOut('D') // read completed

	// Check boot signature to verify read succeeded
	if fs.fatBuffer[510] != 0x55 || fs.fatBuffer[511] != 0xAA {
		return false
	}

	debugOut('E') // signature OK
	return true
}

// ParseBPBStandalone is a standalone function that parses the BPB without method calls.
// Used for RISC-V early boot to avoid allocation issues.
// Returns false on error.
func ParseBPBStandalone(fs *FileSystem) bool {
	debugOut('P') // ParseBPB entered

	// Parse BPB (BIOS Parameter Block)
	fs.bytesPerSec = uint16(fs.fatBuffer[11]) | (uint16(fs.fatBuffer[12]) << 8)
	fs.secPerClus = fs.fatBuffer[13]
	fs.rsvdSecCnt = uint16(fs.fatBuffer[14]) | (uint16(fs.fatBuffer[15]) << 8)
	fs.numFATs = fs.fatBuffer[16]

	debugOut('Q') // parsed basic fields

	// Check for FAT32 (rootEntCnt must be 0 for FAT32)
	rootEntCnt := uint16(fs.fatBuffer[17]) | (uint16(fs.fatBuffer[18]) << 8)
	if rootEntCnt != 0 {
		return false // Not FAT32
	}

	debugOut('R') // FAT32 check OK

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

	debugOut('S') // total sectors parsed

	// FAT32 specific fields (offset 36+)
	fs.fatSize32 = uint32(fs.fatBuffer[36]) |
		(uint32(fs.fatBuffer[37]) << 8) |
		(uint32(fs.fatBuffer[38]) << 16) |
		(uint32(fs.fatBuffer[39]) << 24)

	fs.rootCluster = uint32(fs.fatBuffer[44]) |
		(uint32(fs.fatBuffer[45]) << 8) |
		(uint32(fs.fatBuffer[46]) << 16) |
		(uint32(fs.fatBuffer[47]) << 24)

	debugOut('T') // FAT32 fields parsed

	// Validate
	if fs.bytesPerSec != 512 {
		return false
	}
	if fs.secPerClus == 0 || (fs.secPerClus&(fs.secPerClus-1)) != 0 {
		return false // Must be power of 2
	}
	if fs.numFATs == 0 {
		return false
	}
	if fs.fatSize32 == 0 {
		return false
	}

	debugOut('U') // validation OK

	// Calculate derived values
	fs.fatStartSec = uint64(fs.rsvdSecCnt)
	fs.dataStartSec = fs.fatStartSec + uint64(fs.numFATs)*uint64(fs.fatSize32)
	fs.bytesPerClus = uint32(fs.secPerClus) * uint32(fs.bytesPerSec)

	debugOut('V') // calculations complete
	return true
}

// ParseBPB parses and validates the BIOS Parameter Block.
// Returns false on error (use for early boot to avoid error interfaces).
func (fs *FileSystem) ParseBPB() bool {
	debugOut('P') // ParseBPB entered

	// Parse BPB (BIOS Parameter Block)
	fs.bytesPerSec = uint16(fs.fatBuffer[11]) | (uint16(fs.fatBuffer[12]) << 8)
	fs.secPerClus = fs.fatBuffer[13]
	fs.rsvdSecCnt = uint16(fs.fatBuffer[14]) | (uint16(fs.fatBuffer[15]) << 8)
	fs.numFATs = fs.fatBuffer[16]

	debugOut('Q') // parsed basic fields

	// Check for FAT32 (rootEntCnt must be 0 for FAT32)
	rootEntCnt := uint16(fs.fatBuffer[17]) | (uint16(fs.fatBuffer[18]) << 8)
	if rootEntCnt != 0 {
		return false // Not FAT32
	}

	debugOut('R') // FAT32 check OK

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

	debugOut('S') // total sectors parsed

	// FAT32 specific fields (offset 36+)
	fs.fatSize32 = uint32(fs.fatBuffer[36]) |
		(uint32(fs.fatBuffer[37]) << 8) |
		(uint32(fs.fatBuffer[38]) << 16) |
		(uint32(fs.fatBuffer[39]) << 24)

	fs.rootCluster = uint32(fs.fatBuffer[44]) |
		(uint32(fs.fatBuffer[45]) << 8) |
		(uint32(fs.fatBuffer[46]) << 16) |
		(uint32(fs.fatBuffer[47]) << 24)

	debugOut('T') // FAT32 fields parsed

	// Validate
	if fs.bytesPerSec != 512 {
		return false
	}
	if fs.secPerClus == 0 || (fs.secPerClus&(fs.secPerClus-1)) != 0 {
		return false // Must be power of 2
	}
	if fs.numFATs == 0 {
		return false
	}
	if fs.fatSize32 == 0 {
		return false
	}

	debugOut('U') // validation OK

	// Calculate derived values
	fs.fatStartSec = uint64(fs.rsvdSecCnt)
	fs.dataStartSec = fs.fatStartSec + uint64(fs.numFATs)*uint64(fs.fatSize32)
	fs.bytesPerClus = uint32(fs.secPerClus) * uint32(fs.bytesPerSec)

	debugOut('V') // calculations complete
	return true
}
