// mkfat32 creates a FAT32 disk image with files in the root directory.
//
// Usage: go run tools/mkfat32.go -o output.img file1 [file2 ...]
//
// The resulting image is suitable for use with QEMU's virtio-blk device:
//   -drive file=output.img,if=virtio,format=raw
//
// Features:
//   - FAT32 filesystem with Long File Name (LFN) support
//   - Files placed in root directory
//   - Image size rounded up to next MB
//   - Minimal image size (just big enough for the files)
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sectorSize     = 512
	sectorsPerClus = 8                               // 4KB clusters
	clusterSize    = sectorSize * sectorsPerClus     // 4096 bytes
	reservedSecs   = 32                              // Reserved sectors before FAT
	numFATs        = 2                               // Two FAT copies
	rootCluster    = 2                               // Root directory starts at cluster 2
	fsInfoSector   = 1                               // FSInfo sector location
	backupBootSec  = 6                               // Backup boot sector location
	minClusters    = 65525                           // Minimum clusters for FAT32
)

// FAT32 special values
const (
	fat32EOC      = 0x0FFFFFF8 // End of cluster chain
	fat32Free     = 0x00000000 // Free cluster
	fat32Reserved = 0x0FFFFFF0 // Reserved cluster marker
)

// Directory entry attributes
const (
	attrReadOnly  = 0x01
	attrHidden    = 0x02
	attrSystem    = 0x04
	attrVolumeID  = 0x08
	attrDirectory = 0x10
	attrArchive   = 0x20
	attrLongName  = 0x0F // ReadOnly | Hidden | System | VolumeID
)

type fileEntry struct {
	name string // Original filename
	data []byte // File contents
}

func main() {
	outputFile := flag.String("o", "disk.img", "Output image file")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s -o output.img file1 [file2 ...]\n", os.Args[0])
		os.Exit(1)
	}

	// Read input files
	var files []fileEntry
	var totalDataSize int64

	for _, path := range flag.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
			os.Exit(1)
		}
		name := filepath.Base(path)
		files = append(files, fileEntry{name: name, data: data})
		totalDataSize += int64(len(data))
		fmt.Printf("Adding: %s (%d bytes)\n", name, len(data))
	}

	// Calculate image size
	// We need space for: reserved sectors, 2 FATs, root directory, file data
	// Each file needs: ceiling(size/clusterSize) clusters
	// Plus directory entries (estimate 2KB for root dir with LFN)

	dataClusters := int64(0)
	for _, f := range files {
		clusters := (int64(len(f.data)) + clusterSize - 1) / clusterSize
		if clusters == 0 {
			clusters = 1 // Empty files still need 1 cluster
		}
		dataClusters += clusters
	}
	dataClusters += 1 // Root directory cluster

	// Ensure minimum FAT32 cluster count
	if dataClusters < minClusters {
		dataClusters = minClusters
	}

	// FAT size: 4 bytes per cluster entry
	fatEntries := dataClusters + 2 // +2 for reserved entries 0 and 1
	fatSectors := (fatEntries*4 + sectorSize - 1) / sectorSize

	// Total sectors
	totalSectors := int64(reservedSecs) + fatSectors*numFATs + dataClusters*sectorsPerClus

	// Round up to next MB
	totalBytes := totalSectors * sectorSize
	totalBytes = ((totalBytes + 1024*1024 - 1) / (1024 * 1024)) * 1024 * 1024
	totalSectors = totalBytes / sectorSize

	// Recalculate with final size
	dataSectors := totalSectors - int64(reservedSecs) - fatSectors*numFATs
	totalClusters := dataSectors / sectorsPerClus

	fmt.Printf("Image size: %d MB (%d sectors, %d clusters)\n",
		totalBytes/(1024*1024), totalSectors, totalClusters)

	// Create image buffer
	image := make([]byte, totalBytes)

	// Build boot sector
	buildBootSector(image, uint32(totalSectors), uint32(fatSectors))

	// Build FSInfo sector
	buildFSInfo(image, uint32(totalClusters-1)) // -1 for root dir cluster

	// Copy boot sector to backup location
	copy(image[backupBootSec*sectorSize:], image[:sectorSize])

	// Build FAT tables
	fat1Start := reservedSecs * sectorSize
	fat2Start := fat1Start + int(fatSectors)*sectorSize

	// Initialize FAT
	// Entry 0: media type (0x0FFFFFF8)
	// Entry 1: end of chain marker
	// Entry 2: root directory (end of chain)
	binary.LittleEndian.PutUint32(image[fat1Start+0:], 0x0FFFFFF8)
	binary.LittleEndian.PutUint32(image[fat1Start+4:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(image[fat1Start+8:], fat32EOC) // Root dir

	// Data region start
	dataStart := reservedSecs*sectorSize + int(fatSectors)*sectorSize*numFATs

	// Write files and build directory
	nextCluster := uint32(3) // Start after root dir cluster
	var dirEntries []byte

	for _, f := range files {
		// Calculate clusters needed
		numClusters := (len(f.data) + clusterSize - 1) / clusterSize
		if numClusters == 0 {
			numClusters = 1
		}

		firstCluster := nextCluster

		// Write file data
		for i := 0; i < numClusters; i++ {
			clusterOffset := dataStart + int(nextCluster-2)*clusterSize
			start := i * clusterSize
			end := start + clusterSize
			if end > len(f.data) {
				end = len(f.data)
			}
			if start < len(f.data) {
				copy(image[clusterOffset:], f.data[start:end])
			}

			// Update FAT
			if i < numClusters-1 {
				// Point to next cluster
				binary.LittleEndian.PutUint32(image[fat1Start+int(nextCluster)*4:], nextCluster+1)
			} else {
				// End of chain
				binary.LittleEndian.PutUint32(image[fat1Start+int(nextCluster)*4:], fat32EOC)
			}
			nextCluster++
		}

		// Create directory entries (LFN + short name)
		entries := createDirEntries(f.name, firstCluster, uint32(len(f.data)))
		dirEntries = append(dirEntries, entries...)
	}

	// Write root directory
	rootDirOffset := dataStart + int(rootCluster-2)*clusterSize
	copy(image[rootDirOffset:], dirEntries)

	// Copy FAT1 to FAT2
	copy(image[fat2Start:], image[fat1Start:fat1Start+int(fatSectors)*sectorSize])

	// Write image
	if err := os.WriteFile(*outputFile, image, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", *outputFile, err)
		os.Exit(1)
	}

	fmt.Printf("Created: %s\n", *outputFile)
}

func buildBootSector(image []byte, totalSectors, fatSectors uint32) {
	bs := image[0:512]

	// Jump instruction
	bs[0] = 0xEB
	bs[1] = 0x58 // Jump to boot code
	bs[2] = 0x90 // NOP

	// OEM name
	copy(bs[3:11], []byte("MAZZY   "))

	// BPB (BIOS Parameter Block)
	binary.LittleEndian.PutUint16(bs[11:], sectorSize)     // Bytes per sector
	bs[13] = sectorsPerClus                                 // Sectors per cluster
	binary.LittleEndian.PutUint16(bs[14:], reservedSecs)   // Reserved sectors
	bs[16] = numFATs                                        // Number of FATs
	binary.LittleEndian.PutUint16(bs[17:], 0)              // Root entries (0 for FAT32)
	binary.LittleEndian.PutUint16(bs[19:], 0)              // Total sectors 16-bit (0 for FAT32)
	bs[21] = 0xF8                                           // Media type (fixed disk)
	binary.LittleEndian.PutUint16(bs[22:], 0)              // FAT size 16-bit (0 for FAT32)
	binary.LittleEndian.PutUint16(bs[24:], 63)             // Sectors per track
	binary.LittleEndian.PutUint16(bs[26:], 255)            // Number of heads
	binary.LittleEndian.PutUint32(bs[28:], 0)              // Hidden sectors
	binary.LittleEndian.PutUint32(bs[32:], totalSectors)   // Total sectors 32-bit

	// FAT32 Extended BPB
	binary.LittleEndian.PutUint32(bs[36:], fatSectors)     // FAT size 32-bit
	binary.LittleEndian.PutUint16(bs[40:], 0)              // Ext flags
	binary.LittleEndian.PutUint16(bs[42:], 0)              // FS version
	binary.LittleEndian.PutUint32(bs[44:], rootCluster)    // Root cluster
	binary.LittleEndian.PutUint16(bs[48:], fsInfoSector)   // FSInfo sector
	binary.LittleEndian.PutUint16(bs[50:], backupBootSec)  // Backup boot sector
	// bs[52:64] reserved (zeros)

	bs[64] = 0x80                                           // Drive number
	bs[65] = 0                                              // Reserved
	bs[66] = 0x29                                           // Extended boot signature
	binary.LittleEndian.PutUint32(bs[67:], 0x12345678)     // Volume serial number
	copy(bs[71:82], []byte("MAZZY DISK "))                 // Volume label
	copy(bs[82:90], []byte("FAT32   "))                    // File system type

	// Boot signature
	bs[510] = 0x55
	bs[511] = 0xAA
}

func buildFSInfo(image []byte, freeClusters uint32) {
	fsi := image[fsInfoSector*sectorSize : (fsInfoSector+1)*sectorSize]

	// FSInfo signatures
	binary.LittleEndian.PutUint32(fsi[0:], 0x41615252)    // Lead signature
	binary.LittleEndian.PutUint32(fsi[484:], 0x61417272)  // Struct signature
	binary.LittleEndian.PutUint32(fsi[488:], freeClusters) // Free cluster count
	binary.LittleEndian.PutUint32(fsi[492:], 3)           // Next free cluster hint
	binary.LittleEndian.PutUint16(fsi[510:], 0xAA55)      // Trail signature
}

func createDirEntries(name string, cluster uint32, size uint32) []byte {
	// Generate 8.3 short name
	shortName := make83Name(name)

	// Calculate checksum for LFN
	checksum := lfnChecksum(shortName)

	// Create LFN entries if name doesn't fit in 8.3
	needsLFN := !fits83(name)

	var entries []byte

	if needsLFN {
		// Create LFN entries (in reverse order)
		lfnEntries := makeLFNEntries(name, checksum)
		for i := len(lfnEntries) - 1; i >= 0; i-- {
			entries = append(entries, lfnEntries[i]...)
		}
	}

	// Create short directory entry (32 bytes)
	short := make([]byte, 32)
	copy(short[0:11], shortName[:])
	short[11] = attrArchive                                         // Attributes
	short[12] = 0                                                    // NT reserved
	short[13] = 0                                                    // Create time tenths
	binary.LittleEndian.PutUint16(short[14:], 0)                    // Create time
	binary.LittleEndian.PutUint16(short[16:], 0)                    // Create date
	binary.LittleEndian.PutUint16(short[18:], 0)                    // Last access date
	binary.LittleEndian.PutUint16(short[20:], uint16(cluster>>16))  // High cluster
	binary.LittleEndian.PutUint16(short[22:], 0)                    // Write time
	binary.LittleEndian.PutUint16(short[24:], 0)                    // Write date
	binary.LittleEndian.PutUint16(short[26:], uint16(cluster))      // Low cluster
	binary.LittleEndian.PutUint32(short[28:], size)                 // File size

	entries = append(entries, short...)
	return entries
}

func make83Name(name string) [11]byte {
	var result [11]byte
	for i := range result {
		result[i] = ' '
	}

	// Find extension
	ext := ""
	base := name
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		ext = strings.ToUpper(name[dot+1:])
		base = name[:dot]
	}
	base = strings.ToUpper(base)

	// Copy base (up to 8 chars)
	pos := 0
	for i := 0; i < len(base) && pos < 8; i++ {
		c := base[i]
		if isValid83Char(c) {
			result[pos] = c
			pos++
		}
	}

	// Copy extension (up to 3 chars)
	pos = 8
	for i := 0; i < len(ext) && pos < 11; i++ {
		c := ext[i]
		if isValid83Char(c) {
			result[pos] = c
			pos++
		}
	}

	// If name was mangled, add ~1 suffix
	if !fits83(name) && len(base) > 0 {
		// Truncate and add ~1
		if len(base) > 6 {
			for i := 0; i < 6; i++ {
				if i < len(base) && isValid83Char(base[i]) {
					result[i] = base[i]
				}
			}
		}
		result[6] = '~'
		result[7] = '1'
	}

	return result
}

func fits83(name string) bool {
	// Check if name fits in 8.3 format
	ext := ""
	base := name
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		ext = name[dot+1:]
		base = name[:dot]
	}

	if len(base) > 8 || len(ext) > 3 {
		return false
	}

	// Check for invalid characters
	for i := 0; i < len(base); i++ {
		if !isValid83Char(base[i]) {
			return false
		}
	}
	for i := 0; i < len(ext); i++ {
		if !isValid83Char(ext[i]) {
			return false
		}
	}

	// Check for lowercase (8.3 is uppercase only)
	upper := strings.ToUpper(name)
	if upper != name {
		return false
	}

	return true
}

func isValid83Char(c byte) bool {
	if c >= 'A' && c <= 'Z' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	// Special characters allowed in 8.3
	switch c {
	case '!', '#', '$', '%', '&', '\'', '(', ')', '-', '@', '^', '_', '`', '{', '}', '~':
		return true
	}
	return false
}

func lfnChecksum(name [11]byte) uint8 {
	var sum uint8
	for i := 0; i < 11; i++ {
		sum = ((sum & 1) << 7) + (sum >> 1) + name[i]
	}
	return sum
}

func makeLFNEntries(name string, checksum uint8) [][]byte {
	// Convert to UTF-16LE
	utf16 := make([]uint16, 0, len(name)+1)
	for _, r := range name {
		if r < 0x10000 {
			utf16 = append(utf16, uint16(r))
		} else {
			// Surrogate pair for characters > 0xFFFF
			r -= 0x10000
			utf16 = append(utf16, uint16(0xD800+(r>>10)))
			utf16 = append(utf16, uint16(0xDC00+(r&0x3FF)))
		}
	}
	utf16 = append(utf16, 0) // Null terminator

	// Pad to multiple of 13 characters
	for len(utf16)%13 != 0 {
		utf16 = append(utf16, 0xFFFF)
	}

	numEntries := len(utf16) / 13
	entries := make([][]byte, numEntries)

	for i := 0; i < numEntries; i++ {
		entry := make([]byte, 32)
		charOffset := i * 13

		// Sequence number (1-based, last entry has 0x40 flag)
		seq := byte(i + 1)
		if i == numEntries-1 {
			seq |= 0x40 // Last LFN entry
		}
		entry[0] = seq

		// Characters 1-5 (bytes 1-10)
		for j := 0; j < 5; j++ {
			idx := charOffset + j
			if idx < len(utf16) {
				binary.LittleEndian.PutUint16(entry[1+j*2:], utf16[idx])
			}
		}

		entry[11] = attrLongName // Attribute
		entry[12] = 0            // Type (always 0)
		entry[13] = checksum     // Checksum

		// Characters 6-11 (bytes 14-25)
		for j := 0; j < 6; j++ {
			idx := charOffset + 5 + j
			if idx < len(utf16) {
				binary.LittleEndian.PutUint16(entry[14+j*2:], utf16[idx])
			}
		}

		binary.LittleEndian.PutUint16(entry[26:], 0) // First cluster (always 0)

		// Characters 12-13 (bytes 28-31)
		for j := 0; j < 2; j++ {
			idx := charOffset + 11 + j
			if idx < len(utf16) {
				binary.LittleEndian.PutUint16(entry[28+j*2:], utf16[idx])
			}
		}

		entries[i] = entry
	}

	return entries
}
