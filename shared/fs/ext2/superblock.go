package ext2

import "encoding/binary"

// SuperblockSize is the on-disk size of the superblock.
const SuperblockSize = 1024

// Superblock represents the ext2 superblock (1024 bytes at offset 1024).
type Superblock struct {
	InodesCount      uint32   // Total number of inodes
	BlocksCount      uint32   // Total number of blocks
	RBlocksCount     uint32   // Reserved blocks for superuser
	FreeBlocksCount  uint32   // Free blocks count
	FreeInodesCount  uint32   // Free inodes count
	FirstDataBlock   uint32   // First data block (0 for 4K blocks, 1 for 1K blocks)
	LogBlockSize     uint32   // Block size = 1024 << LogBlockSize
	LogFragSize      uint32   // Fragment size (same as block size)
	BlocksPerGroup   uint32   // Blocks per block group
	FragsPerGroup    uint32   // Fragments per group (= BlocksPerGroup)
	InodesPerGroup   uint32   // Inodes per block group
	MTime            uint32   // Last mount time (Unix timestamp)
	WTime            uint32   // Last write time (Unix timestamp)
	MntCount         uint16   // Mount count since last fsck
	MaxMntCount      uint16   // Max mounts before fsck required
	Magic            uint16   // Magic number: 0xEF53
	State            uint16   // Filesystem state
	Errors           uint16   // Error handling behavior
	MinorRevLevel    uint16   // Minor revision level
	LastCheck        uint32   // Last fsck time
	CheckInterval    uint32   // Max interval between fscks
	CreatorOS        uint32   // OS that created the filesystem
	RevLevel         uint32   // Revision level
	DefResUID        uint16   // Default UID for reserved blocks
	DefResGID        uint16   // Default GID for reserved blocks
	FirstIno         uint32   // First non-reserved inode
	InodeSize        uint16   // Inode size in bytes
	BlockGroupNr     uint16   // Block group this superblock is in
	FeatureCompat    uint32   // Compatible feature flags
	FeatureIncompat  uint32   // Incompatible feature flags
	FeatureROCompat  uint32   // Read-only compatible feature flags
	UUID             [16]byte // Volume UUID
	VolumeName       [16]byte // Volume label (null-terminated)
	LastMounted      [64]byte // Last mount point (null-terminated)
	AlgoBitmap       uint32   // Compression algorithm bitmap
	PreallocBlocks   uint8    // Blocks to preallocate for files
	PreallocDirBlock uint8    // Blocks to preallocate for directories
	Padding1         uint16   // Alignment padding
	JournalUUID      [16]byte // Journal superblock UUID
	JournalInum      uint32   // Journal inode number
	JournalDev       uint32   // Journal device number
	LastOrphan       uint32   // Head of orphan inode list
	HashSeed         [4]uint32 // HTREE hash seed
	DefHashVersion   uint8    // Default hash version
	_                [3]byte  // Padding
	DefaultMountOpts uint32   // Default mount options
	FirstMetaBG      uint32   // First metablock block group
	// Remaining bytes to fill 1024 are reserved (zeros)
}

// Marshal serializes the superblock to a 1024-byte buffer.
func (sb *Superblock) Marshal() [SuperblockSize]byte {
	var buf [SuperblockSize]byte
	le := binary.LittleEndian

	le.PutUint32(buf[0:4], sb.InodesCount)
	le.PutUint32(buf[4:8], sb.BlocksCount)
	le.PutUint32(buf[8:12], sb.RBlocksCount)
	le.PutUint32(buf[12:16], sb.FreeBlocksCount)
	le.PutUint32(buf[16:20], sb.FreeInodesCount)
	le.PutUint32(buf[20:24], sb.FirstDataBlock)
	le.PutUint32(buf[24:28], sb.LogBlockSize)
	le.PutUint32(buf[28:32], sb.LogFragSize)
	le.PutUint32(buf[32:36], sb.BlocksPerGroup)
	le.PutUint32(buf[36:40], sb.FragsPerGroup)
	le.PutUint32(buf[40:44], sb.InodesPerGroup)
	le.PutUint32(buf[44:48], sb.MTime)
	le.PutUint32(buf[48:52], sb.WTime)
	le.PutUint16(buf[52:54], sb.MntCount)
	le.PutUint16(buf[54:56], sb.MaxMntCount)
	le.PutUint16(buf[56:58], sb.Magic)
	le.PutUint16(buf[58:60], sb.State)
	le.PutUint16(buf[60:62], sb.Errors)
	le.PutUint16(buf[62:64], sb.MinorRevLevel)
	le.PutUint32(buf[64:68], sb.LastCheck)
	le.PutUint32(buf[68:72], sb.CheckInterval)
	le.PutUint32(buf[72:76], sb.CreatorOS)
	le.PutUint32(buf[76:80], sb.RevLevel)
	le.PutUint16(buf[80:82], sb.DefResUID)
	le.PutUint16(buf[82:84], sb.DefResGID)

	// Dynamic revision fields (offset 84+)
	le.PutUint32(buf[84:88], sb.FirstIno)
	le.PutUint16(buf[88:90], sb.InodeSize)
	le.PutUint16(buf[90:92], sb.BlockGroupNr)
	le.PutUint32(buf[92:96], sb.FeatureCompat)
	le.PutUint32(buf[96:100], sb.FeatureIncompat)
	le.PutUint32(buf[100:104], sb.FeatureROCompat)
	copy(buf[104:120], sb.UUID[:])
	copy(buf[120:136], sb.VolumeName[:])
	copy(buf[136:200], sb.LastMounted[:])
	le.PutUint32(buf[200:204], sb.AlgoBitmap)

	// Performance hints
	buf[204] = sb.PreallocBlocks
	buf[205] = sb.PreallocDirBlock
	// buf[206:208] = padding (zero)

	// Journaling
	copy(buf[208:224], sb.JournalUUID[:])
	le.PutUint32(buf[224:228], sb.JournalInum)
	le.PutUint32(buf[228:232], sb.JournalDev)
	le.PutUint32(buf[232:236], sb.LastOrphan)

	// Hash seed
	le.PutUint32(buf[236:240], sb.HashSeed[0])
	le.PutUint32(buf[240:244], sb.HashSeed[1])
	le.PutUint32(buf[244:248], sb.HashSeed[2])
	le.PutUint32(buf[248:252], sb.HashSeed[3])

	buf[252] = sb.DefHashVersion
	// buf[253:256] = padding (zero)

	le.PutUint32(buf[256:260], sb.DefaultMountOpts)
	le.PutUint32(buf[260:264], sb.FirstMetaBG)

	return buf
}

// UnmarshalSuperblock reads a superblock from a 1024-byte buffer.
func UnmarshalSuperblock(data []byte) (*Superblock, error) {
	if len(data) < SuperblockSize {
		return nil, ErrInvalidSB
	}

	le := binary.LittleEndian
	sb := &Superblock{}

	sb.InodesCount = le.Uint32(data[0:4])
	sb.BlocksCount = le.Uint32(data[4:8])
	sb.RBlocksCount = le.Uint32(data[8:12])
	sb.FreeBlocksCount = le.Uint32(data[12:16])
	sb.FreeInodesCount = le.Uint32(data[16:20])
	sb.FirstDataBlock = le.Uint32(data[20:24])
	sb.LogBlockSize = le.Uint32(data[24:28])
	sb.LogFragSize = le.Uint32(data[28:32])
	sb.BlocksPerGroup = le.Uint32(data[32:36])
	sb.FragsPerGroup = le.Uint32(data[36:40])
	sb.InodesPerGroup = le.Uint32(data[40:44])
	sb.MTime = le.Uint32(data[44:48])
	sb.WTime = le.Uint32(data[48:52])
	sb.MntCount = le.Uint16(data[52:54])
	sb.MaxMntCount = le.Uint16(data[54:56])
	sb.Magic = le.Uint16(data[56:58])
	sb.State = le.Uint16(data[58:60])
	sb.Errors = le.Uint16(data[60:62])
	sb.MinorRevLevel = le.Uint16(data[62:64])
	sb.LastCheck = le.Uint32(data[64:68])
	sb.CheckInterval = le.Uint32(data[68:72])
	sb.CreatorOS = le.Uint32(data[72:76])
	sb.RevLevel = le.Uint32(data[76:80])
	sb.DefResUID = le.Uint16(data[80:82])
	sb.DefResGID = le.Uint16(data[82:84])

	sb.FirstIno = le.Uint32(data[84:88])
	sb.InodeSize = le.Uint16(data[88:90])
	sb.BlockGroupNr = le.Uint16(data[90:92])
	sb.FeatureCompat = le.Uint32(data[92:96])
	sb.FeatureIncompat = le.Uint32(data[96:100])
	sb.FeatureROCompat = le.Uint32(data[100:104])
	copy(sb.UUID[:], data[104:120])
	copy(sb.VolumeName[:], data[120:136])
	copy(sb.LastMounted[:], data[136:200])
	sb.AlgoBitmap = le.Uint32(data[200:204])

	sb.PreallocBlocks = data[204]
	sb.PreallocDirBlock = data[205]

	copy(sb.JournalUUID[:], data[208:224])
	sb.JournalInum = le.Uint32(data[224:228])
	sb.JournalDev = le.Uint32(data[228:232])
	sb.LastOrphan = le.Uint32(data[232:236])

	sb.HashSeed[0] = le.Uint32(data[236:240])
	sb.HashSeed[1] = le.Uint32(data[240:244])
	sb.HashSeed[2] = le.Uint32(data[244:248])
	sb.HashSeed[3] = le.Uint32(data[248:252])

	sb.DefHashVersion = data[252]

	sb.DefaultMountOpts = le.Uint32(data[256:260])
	sb.FirstMetaBG = le.Uint32(data[260:264])

	if sb.Magic != Magic {
		return nil, ErrNotExt2
	}

	return sb, nil
}
