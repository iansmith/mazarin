package fat32

import "mazzy/kmazarin/console"

// File represents an open file on a FAT32 filesystem
type File struct {
	fs           *FileSystem
	entry        DirEntry
	pos          uint64 // Current read position
	currentClus  uint32 // Current cluster being read
	clusOffset   uint32 // Offset within current cluster
	clusterBuf   []byte // Buffer for current cluster
	clusterValid bool   // Is clusterBuf valid?
}

// Open opens a file by path
// Path should start with "/" and use "/" as separator
func (fs *FileSystem) Open(path string) (*File, error) {
	if len(path) == 0 || path[0] != '/' {
		return nil, ErrInvalidPath
	}

	// Split path into components
	components := splitPath(path)
	if len(components) == 0 {
		return nil, ErrInvalidPath
	}

	// Traverse path
	currentCluster := fs.rootCluster
	for i, component := range components {
		entry, err := fs.LookupDir(currentCluster, component)
		if err != nil {
			return nil, err
		}

		isLast := i == len(components)-1
		if isLast {
			// Found the target
			if entry.IsDir {
				return nil, ErrNotFile
			}
			return &File{
				fs:          fs,
				entry:       *entry,
				pos:         0,
				currentClus: entry.Cluster,
				clusOffset:  0,
				clusterBuf:  make([]byte, fs.bytesPerClus),
			}, nil
		}

		// Not the last component, must be a directory
		if !entry.IsDir {
			return nil, ErrNotFound
		}
		currentCluster = entry.Cluster
	}

	return nil, ErrNotFound
}

// splitPath splits a path into components
func splitPath(path string) []string {
	var components []string
	var current []byte

	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '/' {
			if len(current) > 0 {
				components = append(components, string(current))
				current = nil
			}
		} else {
			current = append(current, c)
		}
	}

	if len(current) > 0 {
		components = append(components, string(current))
	}

	return components
}

// Read reads up to len(buf) bytes from the file
func (f *File) Read(buf []byte) (int, error) {
	if f.entry.IsDir {
		return 0, ErrNotFile
	}

	// Check if we've reached end of file
	if f.pos >= uint64(f.entry.Size) {
		return 0, ErrEndOfFile
	}

	bytesPerClus := uint64(f.fs.bytesPerClus)
	totalRead := 0

	for len(buf) > 0 && f.pos < uint64(f.entry.Size) {
		// Load current cluster if needed
		if !f.clusterValid {
			if f.currentClus == 0 || isEOF(f.currentClus) {
				return totalRead, ErrEndOfFile
			}
			if err := f.fs.readCluster(f.currentClus, f.clusterBuf); err != nil {
				return totalRead, err
			}
			f.clusterValid = true
		}

		// Calculate how much to read from this cluster
		remainingInCluster := uint32(bytesPerClus) - f.clusOffset
		remainingInFile := f.entry.Size - uint32(f.pos)
		toRead := uint32(len(buf))
		if toRead > remainingInCluster {
			toRead = remainingInCluster
		}
		if toRead > remainingInFile {
			toRead = remainingInFile
		}

		// Copy data
		copy(buf[:toRead], f.clusterBuf[f.clusOffset:f.clusOffset+toRead])

		// Update positions
		buf = buf[toRead:]
		f.pos += uint64(toRead)
		f.clusOffset += toRead
		totalRead += int(toRead)

		// Check if we need to move to next cluster
		if f.clusOffset >= uint32(bytesPerClus) {
			nextCluster, err := f.fs.readFATEntry(f.currentClus)
			if err != nil {
				return totalRead, err
			}
			f.currentClus = nextCluster
			f.clusOffset = 0
			f.clusterValid = false
		}
	}

	return totalRead, nil
}

// ReadAll reads the entire file contents
func (f *File) ReadAll() ([]byte, error) {
	console.KPrintf("[ReadAll] start size=%d\r\n", f.entry.Size)
	if f.entry.IsDir {
		return nil, ErrNotFile
	}

	// Seek to beginning
	console.KWriteString("[ReadAll] seeking\r\n")
	if err := f.Seek(0); err != nil {
		return nil, err
	}

	// Allocate buffer for entire file
	console.KPrintf("[ReadAll] allocating %d bytes\r\n", f.entry.Size)
	result := make([]byte, f.entry.Size)
	console.KWriteString("[ReadAll] calling Read\r\n")
	n, err := f.Read(result)
	console.KPrintf("[ReadAll] Read returned n=%d\r\n", n)
	if err != nil && err != ErrEndOfFile {
		return nil, err
	}
	console.KWriteString("[ReadAll] done\r\n")
	return result[:n], nil
}

// Seek moves the file position to offset bytes from the start
func (f *File) Seek(offset uint64) error {
	if offset > uint64(f.entry.Size) {
		return ErrInvalidSeek
	}

	// Calculate which cluster and offset within cluster
	bytesPerClus := uint64(f.fs.bytesPerClus)
	clusterIndex := offset / bytesPerClus
	f.clusOffset = uint32(offset % bytesPerClus)

	// Traverse FAT chain to find the cluster
	f.currentClus = f.entry.Cluster
	for i := uint64(0); i < clusterIndex; i++ {
		if f.currentClus == 0 || isEOF(f.currentClus) {
			return ErrEndOfFile
		}
		nextCluster, err := f.fs.readFATEntry(f.currentClus)
		if err != nil {
			return err
		}
		if isBad(nextCluster) {
			return ErrBadCluster
		}
		f.currentClus = nextCluster
	}

	f.pos = offset
	f.clusterValid = false

	return nil
}

// Size returns the file size in bytes
func (f *File) Size() uint32 {
	return f.entry.Size
}

// Name returns the file name
func (f *File) Name() string {
	return f.entry.Name
}

// Close closes the file (releases resources)
func (f *File) Close() error {
	f.clusterBuf = nil
	f.clusterValid = false
	return nil
}
