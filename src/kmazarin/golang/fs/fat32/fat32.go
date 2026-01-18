// Package fat32 provides a read-only FAT32 filesystem implementation.
package fat32

import (
	"kmazarin/deviceapi"
)

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

// Common errors
var (
	ErrNotFAT32      = &FAT32Error{"not a FAT32 filesystem"}
	ErrInvalidBPB    = &FAT32Error{"invalid BPB"}
	ErrReadFailed    = &FAT32Error{"read failed"}
	ErrNotFound      = &FAT32Error{"file not found"}
	ErrNotFile       = &FAT32Error{"not a file"}
	ErrInvalidPath   = &FAT32Error{"invalid path"}
	ErrEndOfFile     = &FAT32Error{"end of file"}
	ErrBadCluster    = &FAT32Error{"bad cluster"}
	ErrInvalidSeek   = &FAT32Error{"invalid seek position"}
)

// FileSystem represents a mounted FAT32 filesystem
type FileSystem struct {
	device       deviceapi.BlockDevice
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
	// Sector buffer for FAT reads
	fatBuffer [512]byte
}

// Mount mounts a FAT32 filesystem on the given block device
func Mount(device deviceapi.BlockDevice) (*FileSystem, error) {
	if device.BlockSize() != 512 {
		return nil, &FAT32Error{"only 512-byte sectors supported"}
	}

	fs := &FileSystem{
		device: device,
	}

	// Read boot sector (sector 0)
	var bootSector [512]byte
	if err := device.ReadBlock(0, bootSector[:]); err != nil {
		return nil, ErrReadFailed
	}

	// Check boot signature
	if bootSector[510] != 0x55 || bootSector[511] != 0xAA {
		return nil, ErrNotFAT32
	}

	// Parse BPB (BIOS Parameter Block)
	fs.bytesPerSec = uint16(bootSector[11]) | (uint16(bootSector[12]) << 8)
	fs.secPerClus = bootSector[13]
	fs.rsvdSecCnt = uint16(bootSector[14]) | (uint16(bootSector[15]) << 8)
	fs.numFATs = bootSector[16]

	// Check for FAT32 (rootEntCnt must be 0 for FAT32)
	rootEntCnt := uint16(bootSector[17]) | (uint16(bootSector[18]) << 8)
	if rootEntCnt != 0 {
		return nil, ErrNotFAT32
	}

	// Total sectors (16-bit or 32-bit)
	totSec16 := uint16(bootSector[19]) | (uint16(bootSector[20]) << 8)
	if totSec16 != 0 {
		fs.totalSectors = uint32(totSec16)
	} else {
		fs.totalSectors = uint32(bootSector[32]) |
			(uint32(bootSector[33]) << 8) |
			(uint32(bootSector[34]) << 16) |
			(uint32(bootSector[35]) << 24)
	}

	// FAT32 specific fields (offset 36+)
	fs.fatSize32 = uint32(bootSector[36]) |
		(uint32(bootSector[37]) << 8) |
		(uint32(bootSector[38]) << 16) |
		(uint32(bootSector[39]) << 24)

	fs.rootCluster = uint32(bootSector[44]) |
		(uint32(bootSector[45]) << 8) |
		(uint32(bootSector[46]) << 16) |
		(uint32(bootSector[47]) << 24)

	// Validate
	if fs.bytesPerSec != 512 {
		return nil, ErrInvalidBPB
	}
	if fs.secPerClus == 0 || (fs.secPerClus&(fs.secPerClus-1)) != 0 {
		return nil, ErrInvalidBPB // Must be power of 2
	}
	if fs.numFATs == 0 {
		return nil, ErrInvalidBPB
	}
	if fs.fatSize32 == 0 {
		return nil, ErrNotFAT32
	}

	// Calculate derived values
	fs.fatStartSec = uint64(fs.rsvdSecCnt)
	fs.dataStartSec = fs.fatStartSec + uint64(fs.numFATs)*uint64(fs.fatSize32)
	fs.bytesPerClus = uint32(fs.secPerClus) * uint32(fs.bytesPerSec)

	return fs, nil
}

// clusterToSector converts a cluster number to the first sector of that cluster
func (fs *FileSystem) clusterToSector(cluster uint32) uint64 {
	// Clusters 0 and 1 are reserved
	return fs.dataStartSec + uint64(cluster-2)*uint64(fs.secPerClus)
}

// readFATEntry reads a FAT entry for the given cluster
func (fs *FileSystem) readFATEntry(cluster uint32) (uint32, error) {
	// Each FAT32 entry is 4 bytes
	fatOffset := cluster * 4
	fatSector := fs.fatStartSec + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	// Read the FAT sector
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

// isEOF checks if a FAT entry indicates end of chain
func isEOF(entry uint32) bool {
	return entry >= FAT32ClusterEOF
}

// isBad checks if a FAT entry indicates a bad cluster
func isBad(entry uint32) bool {
	return entry == FAT32ClusterBad
}

// RootCluster returns the root directory cluster
func (fs *FileSystem) RootCluster() uint32 {
	return fs.rootCluster
}

// BytesPerCluster returns the cluster size in bytes
func (fs *FileSystem) BytesPerCluster() uint32 {
	return fs.bytesPerClus
}

// readCluster reads an entire cluster into the buffer
func (fs *FileSystem) readCluster(cluster uint32, buf []byte) error {
	if uint32(len(buf)) < fs.bytesPerClus {
		return &FAT32Error{"buffer too small for cluster"}
	}

	startSector := fs.clusterToSector(cluster)
	for i := uint8(0); i < fs.secPerClus; i++ {
		offset := uint32(i) * 512
		if err := fs.device.ReadBlock(startSector+uint64(i), buf[offset:offset+512]); err != nil {
			return ErrReadFailed
		}
	}
	return nil
}
