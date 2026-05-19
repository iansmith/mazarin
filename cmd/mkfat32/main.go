// mkfat32 creates a FAT32 disk image with files and optional subdirectories.
//
// Usage:
//
//	mkfat32 -o output.img file1 [file2 ...]
//	mkfat32 -o output.img -dir /fonts=../path/to/fonts/ file1 [file2 ...]
//	mkfat32 -o output.img -base base.img file1 [file2 ...]
//	mkfat32 -o output.img -pad 300 -dir /fonts=../path/ file1 [file2 ...]
//
// Flags:
//
//	-o        Output image file (default: disk.img)
//	-base     Base image to append files to (copies base, adds files to root)
//	-dir      Add a subdirectory: /mountpoint=hostpath (e.g. /fonts=../fonts/)
//	-pad      Minimum image size in MB (for pre-allocating space in base images)
//
// Features:
//   - FAT32 filesystem with Long File Name (LFN) support
//   - Subdirectory support via -dir flag
//   - Base image append mode via -base flag (avoids rebuilding static content)
//   - Image size rounded up to next MB (or to -pad value)
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
	sectorsPerClus = 8                           // 4KB clusters
	clusterSize    = sectorSize * sectorsPerClus // 4096 bytes
	reservedSecs   = 32                          // Reserved sectors before FAT
	numFATs        = 2                           // Two FAT copies
	rootCluster    = 2                           // Root directory starts at cluster 2
	fsInfoSector   = 1                           // FSInfo sector location
	backupBootSec  = 6                           // Backup boot sector location
	minClusters    = 65525                       // Minimum clusters for FAT32
	entriesPerClus = clusterSize / 32            // 128 dir entries per 4KB cluster
)

// FAT32 special values
const (
	fat32EOC  = 0x0FFFFFF8 // End of cluster chain
	fat32Free = 0x00000000 // Free cluster
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

// imageState tracks the mutable state of an image being built or modified.
type imageState struct {
	image       []byte
	fatStart    int
	fat2Start   int
	dataStart   int
	fatSectors  int64
	totalClus   int64
	nextCluster uint32 // next free cluster to allocate

	// Short name deduplication: maps base short name → next suffix number.
	shortNameCount map[[11]byte]int
}

func main() {
	outputFile := flag.String("o", "disk.img", "Output image file")
	baseFile := flag.String("base", "", "Base image to append files to")
	dirSpec := flag.String("dir", "", "Add directory: /mountpoint=hostpath")
	padMB := flag.Int("pad", 0, "Minimum image size in MB")
	flag.Parse()

	// Read input files (positional args → root directory).
	var rootFiles []fileEntry
	var totalDataSize int64

	for _, path := range flag.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fatalf("Error reading %s: %v\n", path, err)
		}
		name := filepath.Base(path)
		rootFiles = append(rootFiles, fileEntry{name: name, data: data})
		totalDataSize += int64(len(data))
		fmt.Printf("  root: %s (%d bytes)\n", name, len(data))
	}

	// Parse -dir flag if specified.
	var subdirMount, subdirHost string
	var subdirFiles []fileEntry
	if *dirSpec != "" {
		parts := strings.SplitN(*dirSpec, "=", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "/") {
			fatalf("Invalid -dir format. Expected: /mountpoint=hostpath\n")
		}
		subdirMount = strings.TrimPrefix(parts[0], "/")
		subdirHost = parts[1]
		var err error
		subdirFiles, err = readHostDir(subdirHost)
		if err != nil {
			fatalf("Error reading directory %s: %v\n", subdirHost, err)
		}
		for _, f := range subdirFiles {
			totalDataSize += int64(len(f.data))
		}
		fmt.Printf("  dir /%s: %d files from %s\n", subdirMount, len(subdirFiles), subdirHost)
	}

	if *baseFile != "" {
		// ── Base image mode ──────────────────────────────────────────
		if *dirSpec != "" {
			fatalf("-base and -dir cannot be used together\n")
		}
		baseData, err := os.ReadFile(*baseFile)
		if err != nil {
			fatalf("Error reading base image %s: %v\n", *baseFile, err)
		}
		fmt.Printf("Base image: %s (%d MB)\n", *baseFile, len(baseData)/(1024*1024))

		st, err := parseBaseImage(baseData)
		if err != nil {
			fatalf("Error parsing base image: %v\n", err)
		}

		// Add root-level files.
		for _, f := range rootFiles {
			if err := st.addFileToRootDir(f); err != nil {
				fatalf("Error adding %s: %v\n", f.name, err)
			}
		}

		st.finalize()

		if err := os.WriteFile(*outputFile, st.image, 0644); err != nil {
			fatalf("Error writing %s: %v\n", *outputFile, err)
		}
		fmt.Printf("Created: %s (%d MB, base mode)\n", *outputFile, len(st.image)/(1024*1024))
	} else {
		// ── New image mode ───────────────────────────────────────────
		if len(rootFiles) == 0 && len(subdirFiles) == 0 {
			fatalf("No files specified\n")
		}

		st := buildNewImage(rootFiles, subdirMount, subdirFiles, *padMB)

		if err := os.WriteFile(*outputFile, st.image, 0644); err != nil {
			fatalf("Error writing %s: %v\n", *outputFile, err)
		}
		fmt.Printf("Created: %s (%d MB)\n", *outputFile, len(st.image)/(1024*1024))
	}
}

// ─── New image creation ──────────────────────────────────────────────────────

func buildNewImage(rootFiles []fileEntry, subdirName string, subdirFiles []fileEntry, padMB int) *imageState {
	// Calculate total clusters needed.
	dataClusters := int64(1) // root directory cluster
	for _, f := range rootFiles {
		dataClusters += fileClusters(len(f.data))
	}
	if len(subdirFiles) > 0 {
		dataClusters += subdirClusters(len(subdirFiles)) // subdir entry clusters
		for _, f := range subdirFiles {
			dataClusters += fileClusters(len(f.data))
		}
	}
	if dataClusters < minClusters {
		dataClusters = minClusters
	}

	// FAT size.
	fatEntries := dataClusters + 2
	fatSectors := (fatEntries*4 + sectorSize - 1) / sectorSize

	// Total size.
	totalSectors := int64(reservedSecs) + fatSectors*numFATs + dataClusters*sectorsPerClus
	totalBytes := roundUpMB(totalSectors * sectorSize)

	// Apply -pad.
	if padMB > 0 {
		padBytes := int64(padMB) * 1024 * 1024
		if totalBytes < padBytes {
			totalBytes = padBytes
		}
	}

	totalSectors = totalBytes / sectorSize
	dataSectors := totalSectors - int64(reservedSecs) - fatSectors*numFATs
	totalClusters := dataSectors / sectorsPerClus

	fmt.Printf("Image: %d MB (%d clusters)\n", totalBytes/(1024*1024), totalClusters)

	image := make([]byte, totalBytes)

	// Boot sector + FSInfo.
	buildBootSector(image, uint32(totalSectors), uint32(fatSectors))
	buildFSInfo(image, uint32(totalClusters-1))
	copy(image[backupBootSec*sectorSize:], image[:sectorSize])

	// FAT initialization.
	fat1Start := reservedSecs * sectorSize
	binary.LittleEndian.PutUint32(image[fat1Start+0:], 0x0FFFFFF8) // media
	binary.LittleEndian.PutUint32(image[fat1Start+4:], 0xFFFFFFFF) // reserved
	binary.LittleEndian.PutUint32(image[fat1Start+8:], fat32EOC)   // root dir

	dataStart := reservedSecs*sectorSize + int(fatSectors)*sectorSize*numFATs

	st := &imageState{
		image:          image,
		fatStart:       fat1Start,
		fat2Start:      fat1Start + int(fatSectors)*sectorSize,
		dataStart:      dataStart,
		fatSectors:     fatSectors,
		totalClus:      totalClusters,
		nextCluster:    3, // after root dir
		shortNameCount: map[[11]byte]int{},
	}

	var rootDirEntries []byte

	// Create subdirectory if specified.
	if len(subdirFiles) > 0 && subdirName != "" {
		subdirCluster, subdirEntries := st.createSubdir(subdirName, rootCluster, subdirFiles)
		// Add subdir entry to root directory.
		rootDirEntries = append(rootDirEntries, subdirEntries...)
		_ = subdirCluster
	}

	// Write root-level files.
	for _, f := range rootFiles {
		firstCluster := st.writeFileData(f.data)
		entries := st.createDirEntries(f.name, firstCluster, uint32(len(f.data)), attrArchive)
		rootDirEntries = append(rootDirEntries, entries...)
	}

	// Write root directory.
	rootDirOffset := dataStart + int(rootCluster-2)*clusterSize
	if len(rootDirEntries) > clusterSize {
		// Need multi-cluster root directory.
		st.writeMultiClusterDir(rootCluster, rootDirEntries)
	} else {
		copy(image[rootDirOffset:], rootDirEntries)
	}

	st.finalize()
	return st
}

// ─── Base image parsing ──────────────────────────────────────────────────────

func parseBaseImage(data []byte) (*imageState, error) {
	if len(data) < sectorSize {
		return nil, fmt.Errorf("image too small")
	}

	// Check boot signature.
	if data[510] != 0x55 || data[511] != 0xAA {
		return nil, fmt.Errorf("invalid boot signature")
	}

	// Read BPB.
	bps := int(binary.LittleEndian.Uint16(data[11:]))
	spc := int(data[13])
	resvd := int(binary.LittleEndian.Uint16(data[14:]))
	nFATs := int(data[16])
	fatSz := int(binary.LittleEndian.Uint32(data[36:]))
	rootClus := binary.LittleEndian.Uint32(data[44:])
	totalSec := int64(binary.LittleEndian.Uint32(data[32:]))

	if bps != sectorSize || spc != sectorsPerClus {
		return nil, fmt.Errorf("incompatible geometry: bps=%d spc=%d (expected %d/%d)",
			bps, spc, sectorSize, sectorsPerClus)
	}

	clusSize := bps * spc
	fat1Start := resvd * bps
	fat2Start := fat1Start + fatSz*bps
	dataStart := fat1Start + nFATs*fatSz*bps
	dataSectors := totalSec - int64(resvd) - int64(nFATs*fatSz)
	totalClusters := dataSectors / int64(spc)

	// Make a mutable copy.
	image := make([]byte, len(data))
	copy(image, data)

	// Scan FAT1 to find first free cluster.
	nextFree := uint32(0)
	for i := uint32(2); i < uint32(totalClusters)+2; i++ {
		off := fat1Start + int(i)*4
		if off+4 > len(image) {
			break
		}
		entry := binary.LittleEndian.Uint32(image[off:]) & 0x0FFFFFFF
		if entry == fat32Free {
			nextFree = i
			break
		}
	}
	if nextFree == 0 {
		return nil, fmt.Errorf("no free clusters in base image")
	}

	st := &imageState{
		image:          image,
		fatStart:       fat1Start,
		fat2Start:      fat2Start,
		dataStart:      dataStart,
		fatSectors:     int64(fatSz),
		totalClus:      totalClusters,
		nextCluster:    nextFree,
		shortNameCount: map[[11]byte]int{},
	}

	// Scan existing root directory to populate shortNameCount.
	st.scanExistingShortNames(rootClus)

	fmt.Printf("Base: %d clusters total, first free=%d\n", totalClusters, nextFree)
	_ = clusSize
	return st, nil
}

// scanExistingShortNames walks the root directory and records used 8.3 names.
func (st *imageState) scanExistingShortNames(rootClus uint32) {
	chain := st.clusterChain(rootClus)
	for _, clus := range chain {
		off := st.dataStart + int(clus-2)*clusterSize
		for i := 0; i < entriesPerClus; i++ {
			entOff := off + i*32
			if entOff+32 > len(st.image) {
				return
			}
			first := st.image[entOff]
			if first == 0x00 {
				return // end of directory
			}
			if first == 0xE5 {
				continue // deleted
			}
			attr := st.image[entOff+11]
			if attr == attrLongName {
				continue // LFN entry
			}
			// Short name entry — record it.
			var sn [11]byte
			copy(sn[:], st.image[entOff:entOff+11])
			st.shortNameCount[sn] = 1
		}
	}
}

// addFileToRootDir adds a file to the root directory of an existing image.
func (st *imageState) addFileToRootDir(f fileEntry) error {
	firstCluster := st.writeFileData(f.data)
	entries := st.createDirEntries(f.name, firstCluster, uint32(len(f.data)), attrArchive)

	return st.appendToDir(rootCluster, entries)
}

// appendToDir appends directory entries to a directory (identified by its first cluster).
// If the last cluster is full, allocates a new one and chains it.
func (st *imageState) appendToDir(dirCluster uint32, entries []byte) error {
	chain := st.clusterChain(dirCluster)

	// Find the first free entry slot in the directory.
	numEntries := len(entries) / 32

	for _, clus := range chain {
		off := st.dataStart + int(clus-2)*clusterSize
		for i := 0; i <= entriesPerClus-numEntries; i++ {
			entOff := off + i*32
			if st.image[entOff] == 0x00 {
				// Found free slot. Check we have enough consecutive free entries.
				fits := true
				for j := 0; j < numEntries; j++ {
					if st.image[entOff+j*32] != 0x00 {
						fits = false
						break
					}
				}
				if fits {
					copy(st.image[entOff:], entries)
					return nil
				}
			}
		}
	}

	// No space in existing clusters — allocate a new one.
	lastClus := chain[len(chain)-1]
	newClus := st.allocCluster()
	if newClus == 0 {
		return fmt.Errorf("out of clusters")
	}
	// Chain: old EOC → newClus, newClus → EOC.
	binary.LittleEndian.PutUint32(st.image[st.fatStart+int(lastClus)*4:], newClus)
	binary.LittleEndian.PutUint32(st.image[st.fatStart+int(newClus)*4:], fat32EOC)

	// Zero the new cluster (important for dir entry scanning).
	newOff := st.dataStart + int(newClus-2)*clusterSize
	for i := 0; i < clusterSize; i++ {
		st.image[newOff+i] = 0
	}

	// Write entries at start of new cluster.
	copy(st.image[newOff:], entries)
	return nil
}

// ─── Subdirectory creation ───────────────────────────────────────────────────

// createSubdir creates a subdirectory with the given files.
// Returns the subdirectory's first cluster and the dir entry bytes to add to the parent.
func (st *imageState) createSubdir(name string, parentCluster uint32, files []fileEntry) (uint32, []byte) {
	// Allocate cluster for subdir entries.
	subdirCluster := st.allocCluster()
	binary.LittleEndian.PutUint32(st.image[st.fatStart+int(subdirCluster)*4:], fat32EOC)

	// Zero the subdir cluster.
	subdirOff := st.dataStart + int(subdirCluster-2)*clusterSize
	for i := 0; i < clusterSize; i++ {
		st.image[subdirOff+i] = 0
	}

	// Write "." entry.
	dot := makeDotEntry('.', subdirCluster)
	copy(st.image[subdirOff:], dot)

	// Write ".." entry (parent=0 means root for FAT32 convention).
	dotdotClus := parentCluster
	if dotdotClus == rootCluster {
		dotdotClus = 0 // FAT32 convention: ".." in root's child points to cluster 0
	}
	dotdot := makeDotEntry2("..", dotdotClus)
	copy(st.image[subdirOff+32:], dotdot)

	// Write file entries within the subdirectory.
	var subdirEntries []byte
	for _, f := range files {
		firstCluster := st.writeFileData(f.data)
		entries := st.createDirEntries(f.name, firstCluster, uint32(len(f.data)), attrArchive)
		subdirEntries = append(subdirEntries, entries...)
	}

	// Write subdir file entries after "." and "..".
	entryOffset := 64 // 2 × 32 bytes for . and ..
	if len(subdirEntries)+entryOffset > clusterSize {
		// Need multi-cluster subdir. Write what fits, chain the rest.
		st.writeMultiClusterDir(subdirCluster, append(st.image[subdirOff:subdirOff+entryOffset], subdirEntries...))
	} else {
		copy(st.image[subdirOff+entryOffset:], subdirEntries)
	}

	// Create the parent directory entry for this subdirectory.
	parentEntry := st.createDirEntries(name, subdirCluster, 0, attrDirectory)

	return subdirCluster, parentEntry
}

// writeMultiClusterDir writes directory entries that span multiple clusters.
// The first cluster is already allocated (firstCluster). Additional clusters
// are allocated and chained as needed.
func (st *imageState) writeMultiClusterDir(firstCluster uint32, allEntries []byte) {
	currentCluster := firstCluster
	offset := 0
	for offset < len(allEntries) {
		clusterOff := st.dataStart + int(currentCluster-2)*clusterSize
		end := offset + clusterSize
		if end > len(allEntries) {
			end = len(allEntries)
		}
		copy(st.image[clusterOff:], allEntries[offset:end])
		offset = end

		if offset < len(allEntries) {
			// Need another cluster.
			nextClus := st.allocCluster()
			binary.LittleEndian.PutUint32(st.image[st.fatStart+int(currentCluster)*4:], nextClus)
			binary.LittleEndian.PutUint32(st.image[st.fatStart+int(nextClus)*4:], fat32EOC)
			// Zero new cluster.
			newOff := st.dataStart + int(nextClus-2)*clusterSize
			for i := 0; i < clusterSize; i++ {
				st.image[newOff+i] = 0
			}
			currentCluster = nextClus
		}
	}
}

// ─── Image state methods ─────────────────────────────────────────────────────

// allocCluster allocates the next free cluster, updating nextCluster.
func (st *imageState) allocCluster() uint32 {
	c := st.nextCluster
	if int64(c) >= st.totalClus+2 {
		fatalf("Out of clusters (tried to allocate cluster %d, total %d)\n", c, st.totalClus)
	}
	// Scan forward for next free.
	st.nextCluster = c + 1
	for int64(st.nextCluster) < st.totalClus+2 {
		off := st.fatStart + int(st.nextCluster)*4
		if off+4 > len(st.image) {
			break
		}
		entry := binary.LittleEndian.Uint32(st.image[off:]) & 0x0FFFFFFF
		if entry == fat32Free {
			break
		}
		st.nextCluster++
	}
	return c
}

// writeFileData writes file data to sequential clusters, building the FAT chain.
// Returns the first cluster number.
func (st *imageState) writeFileData(data []byte) uint32 {
	numClusters := int(fileClusters(len(data)))
	firstCluster := st.allocCluster()

	currentCluster := firstCluster
	for i := 0; i < numClusters; i++ {
		clusterOffset := st.dataStart + int(currentCluster-2)*clusterSize
		start := i * clusterSize
		end := start + clusterSize
		if end > len(data) {
			end = len(data)
		}
		if start < len(data) {
			copy(st.image[clusterOffset:], data[start:end])
		}

		if i < numClusters-1 {
			nextClus := st.allocCluster()
			binary.LittleEndian.PutUint32(st.image[st.fatStart+int(currentCluster)*4:], nextClus)
			currentCluster = nextClus
		} else {
			binary.LittleEndian.PutUint32(st.image[st.fatStart+int(currentCluster)*4:], fat32EOC)
		}
	}

	return firstCluster
}

// clusterChain returns the chain of clusters starting at cluster c.
func (st *imageState) clusterChain(c uint32) []uint32 {
	var chain []uint32
	current := c
	for {
		chain = append(chain, current)
		off := st.fatStart + int(current)*4
		if off+4 > len(st.image) {
			break
		}
		next := binary.LittleEndian.Uint32(st.image[off:]) & 0x0FFFFFFF
		if next >= 0x0FFFFFF8 {
			break // EOC
		}
		if next < 2 {
			break // invalid
		}
		current = next
		if len(chain) > 1000000 {
			break // safety
		}
	}
	return chain
}

// finalize copies FAT1 to FAT2 and updates FSInfo.
func (st *imageState) finalize() {
	fatSize := int(st.fatSectors) * sectorSize
	copy(st.image[st.fat2Start:st.fat2Start+fatSize], st.image[st.fatStart:st.fatStart+fatSize])

	// Update FSInfo free cluster count and next free hint.
	freeClusters := uint32(0)
	for i := uint32(2); i < uint32(st.totalClus)+2; i++ {
		off := st.fatStart + int(i)*4
		if off+4 > len(st.image) {
			break
		}
		entry := binary.LittleEndian.Uint32(st.image[off:]) & 0x0FFFFFFF
		if entry == fat32Free {
			freeClusters++
		}
	}
	fsi := st.image[fsInfoSector*sectorSize : (fsInfoSector+1)*sectorSize]
	binary.LittleEndian.PutUint32(fsi[488:], freeClusters)
	binary.LittleEndian.PutUint32(fsi[492:], st.nextCluster)
}

// createDirEntries creates LFN + short name directory entries for a file or subdir.
// attr is attrArchive for files, attrDirectory for subdirectories.
func (st *imageState) createDirEntries(name string, cluster uint32, size uint32, attr byte) []byte {
	shortName := st.uniqueShortName(name)
	checksum := lfnChecksum(shortName)
	needsLFN := !fits83(name)

	var entries []byte

	if needsLFN {
		lfnEntries := makeLFNEntries(name, checksum)
		for i := len(lfnEntries) - 1; i >= 0; i-- {
			entries = append(entries, lfnEntries[i]...)
		}
	}

	short := make([]byte, 32)
	copy(short[0:11], shortName[:])
	short[11] = attr
	binary.LittleEndian.PutUint16(short[20:], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(short[26:], uint16(cluster))
	binary.LittleEndian.PutUint32(short[28:], size)

	entries = append(entries, short...)
	return entries
}

// uniqueShortName generates a unique 8.3 name, deduplicating with ~N suffixes.
func (st *imageState) uniqueShortName(name string) [11]byte {
	base := make83Name(name)

	if _, exists := st.shortNameCount[base]; !exists {
		st.shortNameCount[base] = 1
		return base
	}

	// Collision — try ~2, ~3, ... ~999.
	st.shortNameCount[base]++
	for n := st.shortNameCount[base]; n <= 999; n++ {
		candidate := make83NameWithSuffix(name, n)
		if _, exists := st.shortNameCount[candidate]; !exists {
			st.shortNameCount[candidate] = 1
			st.shortNameCount[base] = n
			return candidate
		}
	}

	// Fallback: return the base (will collide, but LFN will still work).
	return base
}

// ─── Utility functions ───────────────────────────────────────────────────────

func readHostDir(hostPath string) ([]fileEntry, error) {
	dirEntries, err := os.ReadDir(hostPath)
	if err != nil {
		return nil, err
	}
	var files []fileEntry
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(hostPath, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, fileEntry{name: e.Name(), data: data})
		fmt.Printf("  dir file: %s (%d bytes)\n", e.Name(), len(data))
	}
	return files, nil
}

func fileClusters(size int) int64 {
	c := (int64(size) + clusterSize - 1) / clusterSize
	if c == 0 {
		c = 1
	}
	return c
}

func subdirClusters(numFiles int) int64 {
	// Estimate: each file needs ~3 dir entries (2 LFN + 1 short), plus "." and "..".
	totalEntries := numFiles*3 + 2
	clusters := (totalEntries + entriesPerClus - 1) / entriesPerClus
	if clusters == 0 {
		clusters = 1
	}
	return int64(clusters)
}

func roundUpMB(bytes int64) int64 {
	return ((bytes + 1024*1024 - 1) / (1024 * 1024)) * 1024 * 1024
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

// ─── Boot sector and FSInfo ──────────────────────────────────────────────────

func buildBootSector(image []byte, totalSectors, fatSectors uint32) {
	bs := image[0:512]

	bs[0] = 0xEB
	bs[1] = 0x58
	bs[2] = 0x90

	copy(bs[3:11], []byte("MAZZY   "))

	binary.LittleEndian.PutUint16(bs[11:], sectorSize)
	bs[13] = sectorsPerClus
	binary.LittleEndian.PutUint16(bs[14:], reservedSecs)
	bs[16] = numFATs
	binary.LittleEndian.PutUint16(bs[17:], 0)
	binary.LittleEndian.PutUint16(bs[19:], 0)
	bs[21] = 0xF8
	binary.LittleEndian.PutUint16(bs[22:], 0)
	binary.LittleEndian.PutUint16(bs[24:], 63)
	binary.LittleEndian.PutUint16(bs[26:], 255)
	binary.LittleEndian.PutUint32(bs[28:], 0)
	binary.LittleEndian.PutUint32(bs[32:], totalSectors)

	binary.LittleEndian.PutUint32(bs[36:], fatSectors)
	binary.LittleEndian.PutUint16(bs[40:], 0)
	binary.LittleEndian.PutUint16(bs[42:], 0)
	binary.LittleEndian.PutUint32(bs[44:], rootCluster)
	binary.LittleEndian.PutUint16(bs[48:], fsInfoSector)
	binary.LittleEndian.PutUint16(bs[50:], backupBootSec)

	bs[64] = 0x80
	bs[65] = 0
	bs[66] = 0x29
	binary.LittleEndian.PutUint32(bs[67:], 0x12345678)
	copy(bs[71:82], []byte("MAZZY DISK "))
	copy(bs[82:90], []byte("FAT32   "))

	bs[510] = 0x55
	bs[511] = 0xAA
}

func buildFSInfo(image []byte, freeClusters uint32) {
	fsi := image[fsInfoSector*sectorSize : (fsInfoSector+1)*sectorSize]

	binary.LittleEndian.PutUint32(fsi[0:], 0x41615252)
	binary.LittleEndian.PutUint32(fsi[484:], 0x61417272)
	binary.LittleEndian.PutUint32(fsi[488:], freeClusters)
	binary.LittleEndian.PutUint32(fsi[492:], 3)
	binary.LittleEndian.PutUint16(fsi[510:], 0xAA55)
}

// ─── "." and ".." directory entries ──────────────────────────────────────────

func makeDotEntry(char byte, cluster uint32) []byte {
	entry := make([]byte, 32)
	entry[0] = char
	for i := 1; i < 11; i++ {
		entry[i] = ' '
	}
	entry[11] = attrDirectory
	binary.LittleEndian.PutUint16(entry[20:], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(entry[26:], uint16(cluster))
	return entry
}

func makeDotEntry2(name string, cluster uint32) []byte {
	entry := make([]byte, 32)
	for i := 0; i < 11; i++ {
		entry[i] = ' '
	}
	for i := 0; i < len(name) && i < 11; i++ {
		entry[i] = name[i]
	}
	entry[11] = attrDirectory
	binary.LittleEndian.PutUint16(entry[20:], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(entry[26:], uint16(cluster))
	return entry
}

// ─── 8.3 short name generation ───────────────────────────────────────────────

func make83Name(name string) [11]byte {
	return make83NameWithSuffix(name, 1)
}

func make83NameWithSuffix(name string, suffix int) [11]byte {
	var result [11]byte
	for i := range result {
		result[i] = ' '
	}

	ext := ""
	base := name
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		ext = strings.ToUpper(name[dot+1:])
		base = name[:dot]
	}
	base = strings.ToUpper(base)

	needsTilde := !fits83(name)

	if needsTilde {
		sfx := fmt.Sprintf("~%d", suffix)
		maxBase := 8 - len(sfx)
		pos := 0
		for i := 0; i < len(base) && pos < maxBase; i++ {
			c := base[i]
			if isValid83Char(c) {
				result[pos] = c
				pos++
			}
		}
		for i, c := range []byte(sfx) {
			result[pos+i] = c
		}
	} else {
		pos := 0
		for i := 0; i < len(base) && pos < 8; i++ {
			c := base[i]
			if isValid83Char(c) {
				result[pos] = c
				pos++
			}
		}
	}

	// Extension.
	pos := 8
	for i := 0; i < len(ext) && pos < 11; i++ {
		c := ext[i]
		if isValid83Char(c) {
			result[pos] = c
			pos++
		}
	}

	return result
}

func fits83(name string) bool {
	name = strings.ToUpper(name)
	ext := ""
	base := name
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		ext = name[dot+1:]
		base = name[:dot]
	}

	if len(base) > 8 || len(ext) > 3 {
		return false
	}

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
	return true
}

func isValid83Char(c byte) bool {
	if c >= 'A' && c <= 'Z' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '(', ')', '-', '@', '^', '_', '`', '{', '}', '~':
		return true
	}
	return false
}

// ─── LFN support ─────────────────────────────────────────────────────────────

func lfnChecksum(name [11]byte) uint8 {
	var sum uint8
	for i := 0; i < 11; i++ {
		sum = ((sum & 1) << 7) + (sum >> 1) + name[i]
	}
	return sum
}

func makeLFNEntries(name string, checksum uint8) [][]byte {
	utf16 := make([]uint16, 0, len(name)+1)
	for _, r := range name {
		if r < 0x10000 {
			utf16 = append(utf16, uint16(r))
		} else {
			r -= 0x10000
			utf16 = append(utf16, uint16(0xD800+(r>>10)))
			utf16 = append(utf16, uint16(0xDC00+(r&0x3FF)))
		}
	}
	utf16 = append(utf16, 0)

	for len(utf16)%13 != 0 {
		utf16 = append(utf16, 0xFFFF)
	}

	numEntries := len(utf16) / 13
	entries := make([][]byte, numEntries)

	for i := 0; i < numEntries; i++ {
		entry := make([]byte, 32)
		charOffset := i * 13

		seq := byte(i + 1)
		if i == numEntries-1 {
			seq |= 0x40
		}
		entry[0] = seq

		for j := 0; j < 5; j++ {
			idx := charOffset + j
			if idx < len(utf16) {
				binary.LittleEndian.PutUint16(entry[1+j*2:], utf16[idx])
			}
		}

		entry[11] = attrLongName
		entry[12] = 0
		entry[13] = checksum

		for j := 0; j < 6; j++ {
			idx := charOffset + 5 + j
			if idx < len(utf16) {
				binary.LittleEndian.PutUint16(entry[14+j*2:], utf16[idx])
			}
		}

		binary.LittleEndian.PutUint16(entry[26:], 0)

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
