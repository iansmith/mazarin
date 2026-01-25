// mkesp creates an EFI System Partition (ESP) image with the standard directory structure.
//
// Usage: mkesp -o esp.img -bootloader diplomat.efi [-kernel kmazarin.elf]
//
// Creates a FAT32 image with:
//   /EFI/BOOT/BOOTX64.EFI  (bootloader - diplomat.efi)
//   /EFI/Linux/kmazarin.elf (optional kernel)
//
// This tool shares FAT32 creation logic with mkfat32 but adds directory support.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	sectorSize     = 512
	sectorsPerClus = 8                           // 4KB clusters
	clusterSize    = sectorSize * sectorsPerClus // 4096 bytes
	reservedSecs   = 32
	numFATs        = 2
	rootCluster    = 2
	fsInfoSector   = 1
	backupBootSec  = 6
	minClusters    = 65525
)

const (
	fat32EOC      = 0x0FFFFFF8
	fat32Free     = 0x00000000
	fat32Reserved = 0x0FFFFFF0
)

const (
	attrReadOnly  = 0x01
	attrHidden    = 0x02
	attrSystem    = 0x04
	attrVolumeID  = 0x08
	attrDirectory = 0x10
	attrArchive   = 0x20
	attrLongName  = 0x0F
)

type dirEntry struct {
	name        string
	isDirectory bool
	data        []byte
	cluster     uint32
	children    []*dirEntry
}

func main() {
	outputFile := flag.String("o", "esp.img", "Output ESP image file")
	bootloader := flag.String("bootloader", "", "Path to bootloader (will be BOOTX64.EFI)")
	kernel := flag.String("kernel", "", "Path to kernel (optional, will be kmazarin.elf)")
	sizeMB := flag.Int("size", 100, "Image size in MB")
	flag.Parse()

	if *bootloader == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -bootloader <path> [-kernel <path>] [-o output.img] [-size MB]\n", os.Args[0])
		os.Exit(1)
	}

	// Read bootloader
	bootloaderData, err := os.ReadFile(*bootloader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading bootloader: %v\n", err)
		os.Exit(1)
	}

	// Read kernel if provided
	var kernelData []byte
	if *kernel != "" {
		kernelData, err = os.ReadFile(*kernel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading kernel: %v\n", err)
			os.Exit(1)
		}
	}

	// Create directory structure
	root := &dirEntry{name: "/", isDirectory: true}
	efiDir := &dirEntry{name: "EFI", isDirectory: true}
	bootDir := &dirEntry{name: "BOOT", isDirectory: true}
	linuxDir := &dirEntry{name: "Linux", isDirectory: true}

	bootx64 := &dirEntry{name: "BOOTX64.EFI", data: bootloaderData}
	bootDir.children = append(bootDir.children, bootx64)

	efiDir.children = append(efiDir.children, bootDir)

	if len(kernelData) > 0 {
		kernelFile := &dirEntry{name: "kmazarin.elf", data: kernelData}
		linuxDir.children = append(linuxDir.children, kernelFile)
		efiDir.children = append(efiDir.children, linuxDir)
	}

	root.children = append(root.children, efiDir)

	// Create ESP image
	if err := createESP(*outputFile, root, *sizeMB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created ESP image: %s\n", *outputFile)
	fmt.Printf("  Size: %d MB\n", *sizeMB)
	fmt.Printf("  /EFI/BOOT/BOOTX64.EFI (%d bytes)\n", len(bootloaderData))
	if len(kernelData) > 0 {
		fmt.Printf("  /EFI/Linux/kmazarin.elf (%d bytes)\n", len(kernelData))
	}
}

func createESP(filename string, root *dirEntry, sizeMB int) error {
	imageSizeBytes := int64(sizeMB) * 1024 * 1024
	totalSectors := uint32(imageSizeBytes / sectorSize)

	// Calculate FAT size more accurately
	// Each FAT entry is 4 bytes, we have 2 FATs
	// Total clusters = (total sectors - reserved - FAT sectors) / sectors per cluster
	// But FAT sectors depends on total clusters, so iterate to find the right value
	var fatSectors uint32 = 1
	for {
		dataSectors := totalSectors - reservedSecs - (numFATs * fatSectors)
		totalClusters := dataSectors / sectorsPerClus
		requiredFatSectors := (totalClusters*4 + sectorSize - 1) / sectorSize

		if requiredFatSectors <= fatSectors {
			break
		}
		fatSectors = requiredFatSectors
	}

	dataSectors := totalSectors - reservedSecs - (numFATs * fatSectors)
	totalClusters := dataSectors / sectorsPerClus

	// Ensure we have enough clusters for FAT32
	if totalClusters < minClusters {
		return fmt.Errorf("image too small for FAT32 (need at least %d clusters, have %d)", minClusters, totalClusters)
	}

	dataStartSector := reservedSecs + (numFATs * fatSectors)

	// Create image
	image := make([]byte, imageSizeBytes)

	// Write boot sector
	writeBootSector(image, totalSectors, fatSectors)

	// Write FSInfo sector
	writeFSInfo(image)

	// Write backup boot sector
	copy(image[backupBootSec*sectorSize:], image[:sectorSize])

	// Initialize FATs
	fat1Start := reservedSecs * sectorSize
	fat2Start := fat1Start + int(fatSectors)*sectorSize

	// FAT entry 0: media descriptor
	binary.LittleEndian.PutUint32(image[fat1Start:], 0x0FFFFFF8)
	binary.LittleEndian.PutUint32(image[fat2Start:], 0x0FFFFFF8)

	// FAT entry 1: EOC marker
	binary.LittleEndian.PutUint32(image[fat1Start+4:], fat32EOC)
	binary.LittleEndian.PutUint32(image[fat2Start+4:], fat32EOC)

	// FAT entry 2: root directory (EOC)
	binary.LittleEndian.PutUint32(image[fat1Start+8:], fat32EOC)
	binary.LittleEndian.PutUint32(image[fat2Start+8:], fat32EOC)

	// Allocate clusters for directory tree
	nextCluster := uint32(3)
	fatSize := int(fatSectors) * sectorSize
	fat := image[fat1Start : fat1Start+fatSize]
	if err := allocateTree(root, fat, &nextCluster); err != nil {
		return err
	}

	// Copy FAT to second copy
	copy(image[fat2Start:fat2Start+fatSize], fat)

	// Write directory entries
	dataStart := int(dataStartSector) * sectorSize
	writeDirectory(image, root, dataStart, 2)

	// Write file
	return os.WriteFile(filename, image, 0644)
}

func allocateTree(dir *dirEntry, fat []byte, nextCluster *uint32) error {
	if !dir.isDirectory {
		return nil
	}

	// Allocate cluster for this directory
	dir.cluster = *nextCluster
	*nextCluster++

	// Allocate for children
	for _, child := range dir.children {
		if child.isDirectory {
			if err := allocateTree(child, fat, nextCluster); err != nil {
				return err
			}
		} else {
			// Allocate clusters for file
			numClusters := (len(child.data) + clusterSize - 1) / clusterSize
			if numClusters == 0 {
				child.cluster = 0
				continue
			}

			child.cluster = *nextCluster
			for i := 0; i < numClusters; i++ {
				cluster := *nextCluster
				*nextCluster++

				// Write FAT entry
				offset := int(cluster) * 4
				if i == numClusters-1 {
					binary.LittleEndian.PutUint32(fat[offset:], fat32EOC)
				} else {
					binary.LittleEndian.PutUint32(fat[offset:], cluster+1)
				}
			}
		}
	}

	// Mark directory cluster as EOC
	offset := int(dir.cluster) * 4
	binary.LittleEndian.PutUint32(fat[offset:], fat32EOC)

	return nil
}

func writeDirectory(image []byte, dir *dirEntry, dataStart int, cluster uint32) {
	offset := dataStart + int(cluster-2)*clusterSize

	// Write "." entry (this directory)
	if cluster != 2 {
		writeShortEntry(image[offset:], ".", attrDirectory, cluster)
		offset += 32
	}

	// Write ".." entry (parent directory)
	if cluster != 2 {
		writeShortEntry(image[offset:], "..", attrDirectory, 0) // 0 means root
		offset += 32
	}

	// Write children
	for _, child := range dir.children {
		name := strings.ToUpper(child.name)
		var attrs uint8 = attrArchive
		if child.isDirectory {
			attrs = attrDirectory
		}

		// Simple 8.3 name handling
		writeShortEntry(image[offset:], name, attrs, child.cluster)
		offset += 32

		// Write file data
		if !child.isDirectory && len(child.data) > 0 {
			writeFileData(image, child.data, dataStart, child.cluster)
		}

		// Recursively write subdirectory
		if child.isDirectory && len(child.children) > 0 {
			writeDirectory(image, child, dataStart, child.cluster)
		}
	}
}

func writeFileData(image []byte, data []byte, dataStart int, startCluster uint32) {
	cluster := startCluster
	offset := 0

	for offset < len(data) {
		clusterOffset := dataStart + int(cluster-2)*clusterSize
		remaining := len(data) - offset
		toWrite := clusterSize
		if remaining < toWrite {
			toWrite = remaining
		}

		copy(image[clusterOffset:], data[offset:offset+toWrite])
		offset += toWrite
		cluster++
	}
}

func writeShortEntry(entry []byte, name string, attrs uint8, startCluster uint32) {
	// Parse name into base and ext
	base := name
	ext := ""
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		base = name[:idx]
		ext = name[idx+1:]
	}

	// Pad to 8.3
	if len(base) > 8 {
		base = base[:8]
	}
	if len(ext) > 3 {
		ext = ext[:3]
	}

	// Write name (8 bytes base + 3 bytes ext)
	for i := 0; i < 11; i++ {
		if i < len(base) {
			entry[i] = base[i]
		} else if i >= 8 && i-8 < len(ext) {
			entry[i] = ext[i-8]
		} else {
			entry[i] = ' '
		}
	}

	// Attributes
	entry[11] = attrs

	// Reserved
	entry[12] = 0

	// Creation time/date (simplified)
	now := time.Now()
	dosTime := uint16((now.Hour() << 11) | (now.Minute() << 5) | (now.Second() / 2))
	dosDate := uint16(((now.Year() - 1980) << 9) | (int(now.Month()) << 5) | now.Day())

	binary.LittleEndian.PutUint16(entry[14:], dosTime)
	binary.LittleEndian.PutUint16(entry[16:], dosDate)
	binary.LittleEndian.PutUint16(entry[18:], dosDate)
	binary.LittleEndian.PutUint16(entry[22:], dosTime)
	binary.LittleEndian.PutUint16(entry[24:], dosDate)

	// Start cluster
	binary.LittleEndian.PutUint16(entry[20:], uint16(startCluster>>16)) // High word
	binary.LittleEndian.PutUint16(entry[26:], uint16(startCluster))     // Low word

	// File size
	binary.LittleEndian.PutUint32(entry[28:], 0) // 0 for directories
}

func writeBootSector(image []byte, totalSectors, fatSectors uint32) {
	bs := image[0:512]

	// Jump instruction
	bs[0] = 0xEB
	bs[1] = 0x58
	bs[2] = 0x90

	// OEM name
	copy(bs[3:], "MSWIN4.1")

	// BPB
	binary.LittleEndian.PutUint16(bs[11:], sectorSize)
	bs[13] = sectorsPerClus
	binary.LittleEndian.PutUint16(bs[14:], reservedSecs)
	bs[16] = numFATs
	binary.LittleEndian.PutUint16(bs[17:], 0) // Root entries (0 for FAT32)
	binary.LittleEndian.PutUint16(bs[19:], 0) // Total sectors 16-bit (0 for FAT32)
	bs[21] = 0xF8                             // Media descriptor
	binary.LittleEndian.PutUint16(bs[22:], 0) // FAT size 16-bit (0 for FAT32)
	binary.LittleEndian.PutUint16(bs[24:], 63)
	binary.LittleEndian.PutUint16(bs[26:], 255)
	binary.LittleEndian.PutUint32(bs[28:], 0) // Hidden sectors
	binary.LittleEndian.PutUint32(bs[32:], totalSectors)

	// FAT32-specific fields
	binary.LittleEndian.PutUint32(bs[36:], fatSectors)
	binary.LittleEndian.PutUint16(bs[40:], 0)
	binary.LittleEndian.PutUint16(bs[42:], 0)
	binary.LittleEndian.PutUint32(bs[44:], rootCluster)
	binary.LittleEndian.PutUint16(bs[48:], fsInfoSector)
	binary.LittleEndian.PutUint16(bs[50:], backupBootSec)

	// Drive number and signature
	bs[64] = 0x80
	bs[66] = 0x29
	binary.LittleEndian.PutUint32(bs[67:], 0x12345678) // Volume ID
	copy(bs[71:], "ESP        ")                       // Volume label
	copy(bs[82:], "FAT32   ")                          // FS type

	// Boot signature
	bs[510] = 0x55
	bs[511] = 0xAA
}

func writeFSInfo(image []byte) {
	fs := image[fsInfoSector*sectorSize : (fsInfoSector+1)*sectorSize]

	// Signatures
	binary.LittleEndian.PutUint32(fs[0:], 0x41615252)
	binary.LittleEndian.PutUint32(fs[484:], 0x61417272)

	// Free cluster count (unknown)
	binary.LittleEndian.PutUint32(fs[488:], 0xFFFFFFFF)

	// Next free cluster (hint)
	binary.LittleEndian.PutUint32(fs[492:], 0xFFFFFFFF)

	// Trail signature
	binary.LittleEndian.PutUint32(fs[508:], 0xAA550000)
}
