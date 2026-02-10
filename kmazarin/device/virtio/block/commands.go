package block

// VirtIO block device protocol structures (VirtIO 1.2 spec, section 5.2)

// VirtIO block request types
const (
	VIRTIO_BLK_T_IN     = 0 // Read from device
	VIRTIO_BLK_T_OUT    = 1 // Write to device
	VIRTIO_BLK_T_FLUSH  = 4 // Cache flush
	VIRTIO_BLK_T_DISCARD = 11 // Discard/TRIM sectors
	VIRTIO_BLK_T_WRITE_ZEROES = 13 // Write zeros
)

// VirtIO block status codes
const (
	VIRTIO_BLK_S_OK     = 0 // Success
	VIRTIO_BLK_S_IOERR  = 1 // I/O error
	VIRTIO_BLK_S_UNSUPP = 2 // Unsupported operation
)

// VirtIOBlockReq is the request header sent to the device
// This is placed in the first descriptor of the chain
type VirtIOBlockReq struct {
	Type   uint32 // Request type (VIRTIO_BLK_T_*)
	_      uint32 // Reserved (must be zero)
	Sector uint64 // First sector (LBA) to read/write
}

// VirtIOBlockStatus is the status byte written by the device
// This is placed in the last descriptor of the chain (VIRTQ_DESC_F_WRITE)
type VirtIOBlockStatus uint8

// VirtIO block device configuration space (read from device BAR)
type VirtIOBlockConfig struct {
	Capacity       uint64 // Device capacity in 512-byte sectors
	SizeMax        uint32 // Maximum segment size
	SegMax         uint32 // Maximum number of segments
	Geometry       VirtIOBlockGeometry
	BlkSize        uint32 // Block size in bytes (typically 512)
	TopologyInfo   VirtIOBlockTopology
	Writeback      uint8  // Writeback mode (0=writethrough, 1=writeback)
	_              uint8  // Reserved
	NumQueues      uint16 // Number of queues (for multiqueue)
	MaxDiscardSectors uint32
	MaxDiscardSeg  uint32
	DiscardSectorAlignment uint32
	MaxWriteZeroesSectors uint32
	MaxWriteZeroesSeg uint32
	WriteZeroesMAYUnmap uint8
	_              [3]uint8 // Reserved
}

type VirtIOBlockGeometry struct {
	Cylinders uint16
	Heads     uint8
	Sectors   uint8
}

type VirtIOBlockTopology struct {
	PhysicalBlockExp   uint8 // Physical block exponent
	AlignmentOffset    uint8 // Alignment offset
	MinIOSize          uint16 // Minimum I/O size
	OptIOSize          uint32 // Optimal I/O size
}
