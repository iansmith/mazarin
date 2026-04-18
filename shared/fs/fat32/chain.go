package fat32

// SectorRun represents a contiguous range of disk sectors (LBAs).
type SectorRun struct {
	StartLBA    uint64
	SectorCount uint64
}

// GetClusterChain walks the FAT chain starting at startCluster and returns
// all clusters in order. Stops at EOF or after maxClusters (0 = no limit).
func (fs *FileSystem) GetClusterChain(startCluster uint32, maxClusters int) ([]uint32, error) {
	var chain []uint32
	cluster := startCluster

	for {
		if cluster < 2 || IsEOF(cluster) || IsBad(cluster) {
			break
		}
		chain = append(chain, cluster)
		if maxClusters > 0 && len(chain) >= maxClusters {
			break
		}
		next, err := fs.ReadFATEntry(cluster)
		if err != nil {
			return chain, err
		}
		cluster = next
	}

	return chain, nil
}

// ClustersToSectorRuns converts a cluster chain to a list of contiguous
// sector runs. Adjacent clusters that map to consecutive LBAs are merged
// into a single run, reducing the number of I/O operations needed.
func (fs *FileSystem) ClustersToSectorRuns(clusters []uint32) []SectorRun {
	if len(clusters) == 0 {
		return nil
	}

	secPerClus := uint64(fs.secPerClus)
	var runs []SectorRun

	startLBA := fs.ClusterToSector(clusters[0])
	runSectors := secPerClus

	for i := 1; i < len(clusters); i++ {
		lba := fs.ClusterToSector(clusters[i])
		// Check if this cluster is contiguous with the current run
		if lba == startLBA+runSectors {
			runSectors += secPerClus
		} else {
			// Emit current run, start new one
			runs = append(runs, SectorRun{StartLBA: startLBA, SectorCount: runSectors})
			startLBA = lba
			runSectors = secPerClus
		}
	}
	// Emit final run
	runs = append(runs, SectorRun{StartLBA: startLBA, SectorCount: runSectors})

	return runs
}

// FileToSectorRuns opens a file by path and returns its sector runs
// (contiguous LBA ranges) without reading any data. This is used by
// the fs shepherd to plan batched DMA reads.
//
// If fileOffset > 0, clusters before the offset are skipped.
// Returns the sector runs and total file size in bytes.
func (fs *FileSystem) FileToSectorRuns(path string, fileOffset uint64) ([]SectorRun, uint32, error) {
	file, err := fs.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	fileSize := file.Size()
	if fileOffset >= uint64(fileSize) {
		return nil, fileSize, nil
	}

	// Walk FAT chain, skip clusters before fileOffset
	bytesPerClus := uint64(fs.bytesPerClus)
	skipClusters := int(fileOffset / bytesPerClus)

	chain, chainErr := fs.GetClusterChain(file.entry.Cluster, 0)
	if chainErr != nil {
		return nil, fileSize, chainErr
	}

	if skipClusters >= len(chain) {
		return nil, fileSize, nil
	}
	chain = chain[skipClusters:]

	runs := fs.ClustersToSectorRuns(chain)
	return runs, fileSize, nil
}
