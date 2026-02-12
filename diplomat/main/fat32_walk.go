// diplomat/main/fat32_walk.go
// Allocation-free FAT32 directory walking for diplomat
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
// Returns on first error or when callback returns false.
func WalkDir(fs *fat32.FileSystem, cluster uint32, callback func(*SimpleDirEntry) bool) error {
	bytesPerClus := fs.BytesPerCluster()
	if bytesPerClus > uint32(len(dirClusterBuf)) {
		return &errClusterTooLarge
	}

	currentCluster := cluster
	for {
		// Read cluster using FileSystem's method
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

			// Skip LFN entries and volume labels for now
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
			dirEntryBuf.Name[dirEntryBuf.NameLen] = 0 // null terminate

			// Parse cluster and size
			fstClusHI := uint32(entry[20]) | (uint32(entry[21]) << 8)
			fstClusLO := uint32(entry[26]) | (uint32(entry[27]) << 8)
			dirEntryBuf.Cluster = (fstClusHI << 16) | fstClusLO
			dirEntryBuf.Size = uint32(entry[28]) |
				(uint32(entry[29]) << 8) |
				(uint32(entry[30]) << 16) |
				(uint32(entry[31]) << 24)
			dirEntryBuf.IsDir = (attr & 0x10) != 0

			// Call the callback
			if !callback(&dirEntryBuf) {
				return nil
			}
		}

		// Follow cluster chain
		nextCluster, err := readFATEntryDirect(fs, currentCluster)
		if err != nil {
			return err
		}
		if nextCluster >= 0x0FFFFFF8 { // EOF
			return nil
		}
		if nextCluster < 2 {
			return &errInvalidClusterChain
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

// readClusterDirectNoError reads a cluster without returning error (for early boot).
// Panics on failure instead of allocating error interface.
func readClusterDirectNoError(fs *fat32.FileSystem, cluster uint32, buf []byte) {
	debugPortOut('r')
	startSector := fs.ClusterToSector(cluster)
	debugPortOut('s')
	secPerClus := fs.BytesPerCluster() / 512
	debugPortOut('p')

	for i := uint32(0); i < secPerClus; i++ {
		debugPortOut('i')
		offset := i * 512
		debugPortOut('o')
		// Use RISC-V specific no-error block read - call directly to avoid write barrier
		debugPortOut('P')
		ReadBlockVirtIONoError(startSector+uint64(i), buf[offset:offset+512])
		debugPortOut('Q')
	}
	debugPortOut('d')
}

// FAT buffer for reading FAT entries
var fatSectorBuf [512]byte
var lastFATSector uint64 = 0xFFFFFFFFFFFFFFFF

// readFATEntryDirect reads a FAT entry directly
func readFATEntryDirect(fs *fat32.FileSystem, cluster uint32) (uint32, error) {
	fatOffset := cluster * 4
	fatSector := fs.FATStartSector() + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	// Only read if it's a different sector
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

// readFATEntryDirectNoError reads a FAT entry without error allocation (for early boot).
func readFATEntryDirectNoError(fs *fat32.FileSystem, cluster uint32) uint32 {
	fatOffset := cluster * 4
	fatSector := fs.FATStartSector() + uint64(fatOffset/512)
	fatEntryOffset := fatOffset % 512

	// Only read if it's a different sector
	if fatSector != lastFATSector {
		ReadBlockVirtIONoError(fatSector, fatSectorBuf[:])
		lastFATSector = fatSector
	}

	entry := uint32(fatSectorBuf[fatEntryOffset]) |
		(uint32(fatSectorBuf[fatEntryOffset+1]) << 8) |
		(uint32(fatSectorBuf[fatEntryOffset+2]) << 16) |
		(uint32(fatSectorBuf[fatEntryOffset+3]) << 24)
	entry &= 0x0FFFFFFF

	return entry
}

// extractLFNChars extracts UTF-16LE characters from an LFN entry field.
// offset: byte offset in LFN entry, count: number of UTF-16 chars to extract
// currentLen: current position in dirEntryBuf.Name
// Returns: new position in dirEntryBuf.Name
func extractLFNChars(lfn []byte, offset int, count int, currentLen uint8) uint8 {
	for i := 0; i < count; i++ {
		byteOff := offset + (i * 2)
		if byteOff+1 >= len(lfn) {
			break
		}
		lo := lfn[byteOff]
		hi := lfn[byteOff+1]

		// End marker (0x0000) or padding (0xFFFF)
		if (lo == 0x00 && hi == 0x00) || (lo == 0xFF && hi == 0xFF) {
			break
		}

		// Only support ASCII (hi byte must be 0)
		if hi == 0 && lo >= 0x20 && lo < 0x7F {
			if currentLen < 255 {
				dirEntryBuf.Name[currentLen] = lo
				currentLen++
			}
		}
	}
	return currentLen
}

// PrintDirEntry prints a SimpleDirEntry using diplomat's printString
func PrintDirEntry(e *SimpleDirEntry) {
	printString("  ")
	if e.IsDir {
		printString("[DIR] ")
	} else {
		printString("      ")
	}
	// Print name from fixed buffer
	for i := uint8(0); i < e.NameLen; i++ {
		printChar(uint16(e.Name[i]))
	}
	printString("\r\n")
}

// LFN entry buffer (up to 20 LFN entries = 255 char filename)
var lfnBuffer [20][32]byte
var lfnCount int

// WalkDirNoError walks a directory without allocating error interfaces (for early boot).
// Panics on failure instead of returning errors.
func WalkDirNoError(fs *fat32.FileSystem, cluster uint32, callback func(*SimpleDirEntry) bool) {
	debugPortOut('W')
	bytesPerClus := fs.BytesPerCluster()
	debugPortOut('B')
	if bytesPerClus > uint32(len(dirClusterBuf)) {
		printString("ERROR: Cluster too large\r\n")
		for {}
	}

	debugPortOut('C')
	currentCluster := cluster
	lfnCount = 0 // Reset LFN collection

	for {
		debugPortOut('R')
		// Read cluster using no-error version
		readClusterDirectNoError(fs, currentCluster, dirClusterBuf[:bytesPerClus])

		// Process directory entries (32 bytes each)
		for offset := uint32(0); offset < bytesPerClus; offset += 32 {
			entry := dirClusterBuf[offset : offset+32]

			// End of directory
			if entry[0] == 0x00 {
				return
			}

			// Deleted entry
			if entry[0] == 0xE5 {
				lfnCount = 0 // Reset LFN collection
				continue
			}

			attr := entry[11]

			// LFN entry - collect it
			if attr == 0x0F {
				if lfnCount < len(lfnBuffer) {
					copy(lfnBuffer[lfnCount][:], entry)
					lfnCount++
				}
				continue
			}

			// Skip volume labels
			if (attr & 0x08) != 0 {
				continue
			}

			// Build name - use LFN if available, otherwise 8.3
			if lfnCount > 0 {
				// Build long filename from LFN entries (in reverse order)
				dirEntryBuf.NameLen = 0
				for i := lfnCount - 1; i >= 0; i-- {
					lfn := lfnBuffer[i][:]
					// LFN has 3 name fields: 1-10 (5 chars), 14-25 (6 chars), 28-31 (2 chars)
					// Each char is UTF-16LE (2 bytes)
					dirEntryBuf.NameLen = extractLFNChars(lfn, 1, 5, dirEntryBuf.NameLen)
					dirEntryBuf.NameLen = extractLFNChars(lfn, 14, 6, dirEntryBuf.NameLen)
					dirEntryBuf.NameLen = extractLFNChars(lfn, 28, 2, dirEntryBuf.NameLen)
				}
				dirEntryBuf.Name[dirEntryBuf.NameLen] = 0 // null terminate
				lfnCount = 0 // Reset for next entry
			} else {
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
				dirEntryBuf.Name[dirEntryBuf.NameLen] = 0 // null terminate
			}

			// Parse cluster and size
			clusterLo := uint32(entry[26]) | uint32(entry[27])<<8
			clusterHi := uint32(entry[20]) | uint32(entry[21])<<8
			dirEntryBuf.Cluster = (clusterHi << 16) | clusterLo

			dirEntryBuf.Size = uint32(entry[28]) | uint32(entry[29])<<8 |
				uint32(entry[30])<<16 | uint32(entry[31])<<24

			dirEntryBuf.IsDir = (attr & 0x10) != 0

			// Call callback
			if !callback(&dirEntryBuf) {
				return
			}
		}

		// TODO: Handle chain traversal for large directories
		// For now, assume directory fits in one cluster
		return
	}
}
