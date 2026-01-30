// cardinal/main/fat32_walk.go
// Allocation-free FAT32 directory walking for cardinal
package main

import (
	"mazzy/shared/fs/fat32"
)

// SimpleDirEntry is a fixed-size directory entry (no string allocation)
type SimpleDirEntry struct {
	Name    [256]byte // Fixed buffer for name (255 chars + null)
	NameLen uint8
	Cluster uint32
	Size    uint32
	IsDir   bool
}

// Global buffers for directory reading
var (
	dirClusterBuf [4096]byte // Supports up to 4KB clusters (8 sectors)
	dirEntryBuf   SimpleDirEntry
)

// WalkDir calls the callback for each entry in a directory.
// Uses global buffers to avoid allocation.
func WalkDir(fs *fat32.FileSystem, cluster uint32, callback func(*SimpleDirEntry) bool) error {
	bytesPerClus := fs.BytesPerCluster()
	if bytesPerClus > uint32(len(dirClusterBuf)) {
		return &cardinalBootError{"cluster size too large"}
	}

	currentCluster := cluster
	for {
		// Read cluster
		if err := readClusterDirect(fs, currentCluster, dirClusterBuf[:bytesPerClus]); err != nil {
			return err
		}

		// Process directory entries (32 bytes each)
		for offset := uint32(0); offset < bytesPerClus; offset += 32 {
			entry := dirClusterBuf[offset : offset+32]

			// End of directory
			if entry[0] == 0x00 {
				return nil
			}

			// Deleted entry
			if entry[0] == 0xE5 {
				continue
			}

			attr := entry[11]

			// Skip LFN entries and volume labels
			if attr == 0x0F || (attr&0x08) != 0 {
				continue
			}

			// Parse short 8.3 name
			dirEntryBuf.NameLen = 0
			for i := 0; i < 8; i++ {
				c := entry[i]
				if c == ' ' {
					break
				}
				dirEntryBuf.Name[dirEntryBuf.NameLen] = c
				dirEntryBuf.NameLen++
			}

			// Add extension if present
			if entry[8] != ' ' {
				dirEntryBuf.Name[dirEntryBuf.NameLen] = '.'
				dirEntryBuf.NameLen++
				for i := 8; i < 11; i++ {
					c := entry[i]
					if c == ' ' {
						break
					}
					dirEntryBuf.Name[dirEntryBuf.NameLen] = c
					dirEntryBuf.NameLen++
				}
			}
			dirEntryBuf.Name[dirEntryBuf.NameLen] = 0

			// Parse cluster and size
			fstClusHI := uint32(entry[20]) | (uint32(entry[21]) << 8)
			fstClusLO := uint32(entry[26]) | (uint32(entry[27]) << 8)
			dirEntryBuf.Cluster = (fstClusHI << 16) | fstClusLO
			dirEntryBuf.Size = uint32(entry[28]) |
				(uint32(entry[29]) << 8) |
				(uint32(entry[30]) << 16) |
				(uint32(entry[31]) << 24)
			dirEntryBuf.IsDir = (attr & 0x10) != 0

			if !callback(&dirEntryBuf) {
				return nil
			}
		}

		// Follow cluster chain
		nextCluster, err := readFATEntryDirect(fs, currentCluster)
		if err != nil {
			return err
		}
		if nextCluster >= 0x0FFFFFF8 {
			return nil
		}
		if nextCluster < 2 {
			return &cardinalBootError{"invalid cluster chain"}
		}
		currentCluster = nextCluster
	}
}

// readClusterDirect reads a cluster using the FileSystem's device
func readClusterDirect(fs *fat32.FileSystem, cluster uint32, buf []byte) error {
	startSector := fs.ClusterToSector(cluster)
	secPerClus := fs.BytesPerCluster() / 512

	for i := uint32(0); i < secPerClus; i++ {
		offset := i * 512
		if err := fs.Device().ReadBlock(startSector+uint64(i), buf[offset:offset+512]); err != nil {
			return err
		}
	}
	return nil
}

// FAT buffer for reading FAT entries
var fatSectorBuf [512]byte
var lastFATSector uint64 = 0xFFFFFFFFFFFFFFFF

// readFATEntryDirect reads a FAT entry directly
func readFATEntryDirect(fs *fat32.FileSystem, cluster uint32) (uint32, error) {
	fatOffset := cluster * 4
	fatSector := fs.FATStartSector() + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	if fatSector != lastFATSector {
		if err := fs.Device().ReadBlock(fatSector, fatSectorBuf[:]); err != nil {
			return 0, err
		}
		lastFATSector = fatSector
	}

	entry := uint32(fatSectorBuf[fatEntryOffset]) |
		(uint32(fatSectorBuf[fatEntryOffset+1]) << 8) |
		(uint32(fatSectorBuf[fatEntryOffset+2]) << 16) |
		(uint32(fatSectorBuf[fatEntryOffset+3]) << 24)
	entry &= 0x0FFFFFFF

	return entry, nil
}

// matchName compares a directory entry name with a target (case-insensitive)
func matchName(e *SimpleDirEntry, target string) bool {
	if int(e.NameLen) != len(target) {
		return false
	}
	for i := 0; i < len(target); i++ {
		a := e.Name[i]
		b := target[i]
		if a >= 'a' && a <= 'z' {
			a -= 32
		}
		if b >= 'a' && b <= 'z' {
			b -= 32
		}
		if a != b {
			return false
		}
	}
	return true
}
