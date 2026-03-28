// Package ext2 provides ext2 filesystem structures and utilities.
// It defines the on-disk format for creating (cmd/mkext2) and
// reading/writing (runtime fs shepherd) ext2 filesystems.
package ext2

// Superblock magic number
const Magic = 0xEF53

// Superblock offset from start of disk (always byte 1024)
const SuperblockOffset = 1024

// Block sizes: BlockSize = 1024 << LogBlockSize
const (
	LogBlockSize1K = 0 // 1024 bytes
	LogBlockSize2K = 1 // 2048 bytes
	LogBlockSize4K = 2 // 4096 bytes
)

// BlockSize returns the block size for a given log value.
func BlockSize(logBlockSize uint32) uint32 {
	return 1024 << logBlockSize
}

// Reserved inode numbers
const (
	InodeBadBlocks  = 1  // Bad blocks inode
	InodeRoot       = 2  // Root directory inode
	InodeACLIndex   = 3  // ACL index (ext3)
	InodeACLData    = 4  // ACL data (ext3)
	InodeBootLoader = 5  // Boot loader inode
	InodeUndelete   = 6  // Undelete directory inode
	InodeFirstNonRs = 11 // First non-reserved inode (dynamic rev)
)

// Inode mode: file type (upper 4 bits of 16-bit mode)
const (
	TypeMask = 0xF000
	TypeFIFO = 0x1000
	TypeChar = 0x2000
	TypeDir  = 0x4000
	TypeBlk  = 0x6000
	TypeFile = 0x8000
	TypeLink = 0xA000
	TypeSock = 0xC000
)

// Inode mode: permissions (lower 12 bits)
const (
	PermSetUID  = 0x0800
	PermSetGID  = 0x0400
	PermSticky  = 0x0200
	PermOwnerR  = 0x0100
	PermOwnerW  = 0x0080
	PermOwnerX  = 0x0040
	PermGroupR  = 0x0020
	PermGroupW  = 0x0010
	PermGroupX  = 0x0008
	PermOtherR  = 0x0004
	PermOtherW  = 0x0002
	PermOtherX  = 0x0001
	PermOwnerRW = PermOwnerR | PermOwnerW
	PermAll755  = PermOwnerR | PermOwnerW | PermOwnerX | PermGroupR | PermGroupX | PermOtherR | PermOtherX
	PermAll644  = PermOwnerR | PermOwnerW | PermGroupR | PermOtherR
)

// Directory entry file types (stored in DirEntry.FileType)
const (
	FTUnknown = 0
	FTFile    = 1
	FTDir     = 2
	FTChar    = 3
	FTBlk     = 4
	FTFifo    = 5
	FTSock    = 6
	FTLink    = 7
)

// Filesystem state (Superblock.State)
const (
	StateClean  = 1
	StateErrors = 2
)

// Error handling behavior (Superblock.Errors)
const (
	ErrorsContinue = 1
	ErrorsRO       = 2
	ErrorsPanic    = 3
)

// Revision levels (Superblock.RevLevel)
const (
	RevOld     = 0 // Original ext2
	RevDynamic = 1 // Dynamic inode sizes, feature flags
)

// Creator OS (Superblock.CreatorOS)
const (
	OSLinux   = 0
	OSHurd    = 1
	OSMasix   = 2
	OSFreeBSD = 3
	OSLites   = 4
)

// Compatible feature flags (can mount even if unknown flags set)
const (
	FeatureCompatDirPrealloc = 0x0001
	FeatureCompatHasJournal  = 0x0004
	FeatureCompatExtAttr     = 0x0008
	FeatureCompatResizeIno   = 0x0010
	FeatureCompatDirIndex    = 0x0020
)

// Incompatible feature flags (must refuse to mount if unknown flags set)
const (
	FeatureIncompatCompression = 0x0001
	FeatureIncompatFileType    = 0x0002 // Dir entries have file type
	FeatureIncompatRecover     = 0x0004
	FeatureIncompatJournalDev  = 0x0008
	FeatureIncompatMetaBG      = 0x0010
)

// Read-only compatible feature flags (can mount read-only if unknown flags set)
const (
	FeatureROCompatSparseSuper = 0x0001
	FeatureROCompatLargeFile   = 0x0002
	FeatureROCompatBTreeDir    = 0x0004
)

// Inode flags
const (
	InodeFlagSecureDel  = 0x00000001
	InodeFlagUndelete   = 0x00000002
	InodeFlagCompressed = 0x00000004
	InodeFlagSync       = 0x00000008
	InodeFlagImmutable  = 0x00000010
	InodeFlagAppendOnly = 0x00000020
	InodeFlagNoDump     = 0x00000040
	InodeFlagNoAtime    = 0x00000080
)

// Ext2Error represents an ext2-specific error
type Ext2Error struct {
	msg string
}

func (e *Ext2Error) Error() string {
	return "ext2: " + e.msg
}

// Pre-allocated errors
var (
	ErrNotExt2      = &Ext2Error{"not an ext2 filesystem"}
	ErrInvalidSB    = &Ext2Error{"invalid superblock"}
	ErrReadFailed   = &Ext2Error{"read failed"}
	ErrWriteFailed  = &Ext2Error{"write failed"}
	ErrNotFound     = &Ext2Error{"not found"}
	ErrNotFile      = &Ext2Error{"not a file"}
	ErrNotDir       = &Ext2Error{"not a directory"}
	ErrInvalidPath  = &Ext2Error{"invalid path"}
	ErrNoSpace      = &Ext2Error{"no space left"}
	ErrNoInodes     = &Ext2Error{"no inodes available"}
	ErrInvalidInode = &Ext2Error{"invalid inode"}
	ErrNameTooLong  = &Ext2Error{"name too long"}
	ErrExists       = &Ext2Error{"already exists"}
	ErrNotEmpty     = &Ext2Error{"directory not empty"}
	ErrCorrupted    = &Ext2Error{"filesystem corrupted"}
)
