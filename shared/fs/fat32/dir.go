package fat32

// Directory entry attributes
const (
	AttrReadOnly  = 0x01
	AttrHidden    = 0x02
	AttrSystem    = 0x04
	AttrVolumeID  = 0x08
	AttrDirectory = 0x10
	AttrArchive   = 0x20
	AttrLongName  = 0x0F // Combination: ReadOnly | Hidden | System | VolumeID
)

// DirEntry represents a directory entry
type DirEntry struct {
	Name    string
	Cluster uint32
	Size    uint32
	IsDir   bool
	Attr    uint8
}

// shortDirEntry is the 32-byte FAT directory entry structure
type shortDirEntry struct {
	Name       [11]byte // 8.3 filename
	Attr       uint8    // Attributes
	NTRes      uint8    // Reserved for Windows NT
	CrtTimeTth uint8    // Creation time tenths
	CrtTime    uint16   // Creation time
	CrtDate    uint16   // Creation date
	LstAccDate uint16   // Last access date
	FstClusHI  uint16   // High word of first cluster
	WrtTime    uint16   // Write time
	WrtDate    uint16   // Write date
	FstClusLO  uint16   // Low word of first cluster
	FileSize   uint32   // File size
}

// lfnEntry is the 32-byte LFN entry structure
type lfnEntry struct {
	Ord      uint8     // Sequence number
	Name1    [10]byte  // Characters 1-5 (UTF-16LE)
	Attr     uint8     // Always 0x0F
	Type     uint8     // Always 0
	Chksum   uint8     // Checksum of 8.3 name
	Name2    [12]byte  // Characters 6-11 (UTF-16LE)
	FstClusL uint16    // Always 0
	Name3    [4]byte   // Characters 12-13 (UTF-16LE)
}

// ReadDir reads all entries from a directory cluster chain
func (fs *FileSystem) ReadDir(cluster uint32) ([]DirEntry, error) {
	var entries []DirEntry
	buf := make([]byte, fs.bytesPerClus)

	// Collect LFN entries as we go
	var lfnParts []lfnEntry

	currentCluster := cluster
	for {
		// Read cluster
		if err := fs.ReadCluster(currentCluster, buf); err != nil {
			return nil, err
		}

		// Process directory entries (32 bytes each)
		for offset := uint32(0); offset < fs.bytesPerClus; offset += 32 {
			entry := buf[offset : offset+32]

			// End of directory
			if entry[0] == 0x00 {
				return entries, nil
			}

			// Deleted entry
			if entry[0] == 0xE5 {
				lfnParts = nil // Reset LFN collection
				continue
			}

			attr := entry[11]

			// LFN entry
			if attr == AttrLongName {
				var lfn lfnEntry
				lfn.Ord = entry[0]
				copy(lfn.Name1[:], entry[1:11])
				lfn.Attr = entry[11]
				lfn.Type = entry[12]
				lfn.Chksum = entry[13]
				copy(lfn.Name2[:], entry[14:26])
				lfn.FstClusL = uint16(entry[26]) | (uint16(entry[27]) << 8)
				copy(lfn.Name3[:], entry[28:32])

				// Insert at beginning (LFN entries are in reverse order)
				lfnParts = append([]lfnEntry{lfn}, lfnParts...)
				continue
			}

			// Skip volume label
			if attr&AttrVolumeID != 0 {
				lfnParts = nil
				continue
			}

			// Regular short entry
			var de DirEntry
			de.Attr = attr
			de.IsDir = (attr & AttrDirectory) != 0

			// Get cluster
			fstClusHI := uint16(entry[20]) | (uint16(entry[21]) << 8)
			fstClusLO := uint16(entry[26]) | (uint16(entry[27]) << 8)
			de.Cluster = (uint32(fstClusHI) << 16) | uint32(fstClusLO)

			// Get size
			de.Size = uint32(entry[28]) |
				(uint32(entry[29]) << 8) |
				(uint32(entry[30]) << 16) |
				(uint32(entry[31]) << 24)

			// Build name
			if len(lfnParts) > 0 {
				// Use LFN
				de.Name = buildLFN(lfnParts)
			} else {
				// Use 8.3 name
				var name83 [11]byte
				copy(name83[:], entry[0:11])
				de.Name = parse83Name(name83)
			}

			lfnParts = nil // Reset for next entry

			// Skip . and .. entries
			if de.Name != "." && de.Name != ".." {
				entries = append(entries, de)
			}
		}

		// Get next cluster in chain
		nextCluster, err := fs.ReadFATEntry(currentCluster)
		if err != nil {
			return nil, err
		}
		if IsEOF(nextCluster) {
			break
		}
		if IsBad(nextCluster) {
			return nil, ErrBadCluster
		}
		currentCluster = nextCluster
	}

	return entries, nil
}

// buildLFN builds a long filename from LFN entries
func buildLFN(parts []lfnEntry) string {
	var result []byte

	for _, part := range parts {
		// Extract UTF-16LE characters from Name1, Name2, Name3
		// and convert to ASCII (we only support ASCII subset)
		result = appendUTF16Chars(result, part.Name1[:], 5)
		result = appendUTF16Chars(result, part.Name2[:], 6)
		result = appendUTF16Chars(result, part.Name3[:], 2)
	}

	// Trim trailing 0xFF padding and null terminator
	for len(result) > 0 && (result[len(result)-1] == 0xFF || result[len(result)-1] == 0x00) {
		result = result[:len(result)-1]
	}

	return string(result)
}

// appendUTF16Chars extracts UTF-16LE characters and appends ASCII to result
func appendUTF16Chars(result []byte, data []byte, count int) []byte {
	for i := 0; i < count; i++ {
		offset := i * 2
		if offset+1 >= len(data) {
			break
		}
		lo := data[offset]
		hi := data[offset+1]

		// End marker
		if lo == 0x00 && hi == 0x00 {
			break
		}
		// Padding
		if lo == 0xFF && hi == 0xFF {
			break
		}

		// Only support ASCII (hi byte must be 0)
		if hi == 0 && lo >= 0x20 && lo < 0x7F {
			result = append(result, lo)
		} else {
			// Replace non-ASCII with underscore
			result = append(result, '_')
		}
	}
	return result
}

// parse83Name converts an 8.3 filename to a string
func parse83Name(name [11]byte) string {
	var result []byte

	// Base name (first 8 characters)
	for i := 0; i < 8; i++ {
		c := name[i]
		if c == ' ' {
			break
		}
		// Convert to lowercase for consistency
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		result = append(result, c)
	}

	// Extension (last 3 characters)
	if name[8] != ' ' {
		result = append(result, '.')
		for i := 8; i < 11; i++ {
			c := name[i]
			if c == ' ' {
				break
			}
			// Convert to lowercase
			if c >= 'A' && c <= 'Z' {
				c = c - 'A' + 'a'
			}
			result = append(result, c)
		}
	}

	return string(result)
}

// lfnChecksum calculates the checksum for an 8.3 filename
func lfnChecksum(name [11]byte) uint8 {
	var sum uint8
	for i := 0; i < 11; i++ {
		// Rotate right and add
		sum = ((sum & 1) << 7) + (sum >> 1) + name[i]
	}
	return sum
}

// LookupDir finds an entry in a directory by name
func (fs *FileSystem) LookupDir(cluster uint32, name string) (*DirEntry, error) {
	entries, err := fs.ReadDir(cluster)
	if err != nil {
		return nil, err
	}

	// Case-insensitive comparison
	nameLower := toLower(name)
	for i := range entries {
		if toLower(entries[i].Name) == nameLower {
			return &entries[i], nil
		}
	}

	return nil, ErrNotFound
}

// toLower converts a string to lowercase (ASCII only)
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		result[i] = c
	}
	return string(result)
}
