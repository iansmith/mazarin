package net

import "unsafe"

// VirtIONetHdr is the per-packet header prepended to every TX and RX buffer.
// Modern VirtIO 1.0+ layout is fixed at 12 bytes — NumBuffers is always present
// once VIRTIO_F_VERSION_1 is negotiated (spec §5.1.6). The 10-byte legacy variant
// (no NumBuffers) is not supported: net is modern-only, like the block driver.
type VirtIONetHdr struct {
	Flags      uint8  // VIRTIO_NET_HDR_F_* bit flags
	GSOType    uint8  // VIRTIO_NET_HDR_GSO_* segmentation-offload type
	HdrLen     uint16 // Length of headers to replicate across GSO segments
	GSOSize    uint16 // Bytes per GSO segment
	CSumStart  uint16 // Offset where the device begins the checksum
	CSumOffset uint16 // Offset (from CSumStart) where the checksum is stored
	NumBuffers uint16 // Merged RX descriptor count (device-written on RX)
}

// Compile-time assertion: modern VirtIONetHdr is exactly 12 bytes. Build fails
// here if the layout drifts, rather than corrupting DMA at runtime.
var _ [12]byte = [unsafe.Sizeof(VirtIONetHdr{})]byte{}

// VirtIONetHdr.Flags bits
const (
	VIRTIO_NET_HDR_F_NEEDS_CSUM = 1 // Device must compute/insert the checksum
	VIRTIO_NET_HDR_F_DATA_VALID = 2 // Checksum already verified by device
	VIRTIO_NET_HDR_F_RSC_INFO   = 4 // Header carries RSC info
)

// VirtIONetHdr.GSOType values
const (
	VIRTIO_NET_HDR_GSO_NONE  = 0
	VIRTIO_NET_HDR_GSO_TCPV4 = 1
	VIRTIO_NET_HDR_GSO_UDP   = 3
	VIRTIO_NET_HDR_GSO_TCPV6 = 4
	VIRTIO_NET_HDR_GSO_ECN   = 0x80 // OR-ed flag: ECN present
)

// Net feature bits — low feature page (bits 0-31), Handshake's `low` argument.
const (
	VIRTIO_NET_F_CSUM                = 1 << 0
	VIRTIO_NET_F_GUEST_CSUM          = 1 << 1
	VIRTIO_NET_F_CTRL_GUEST_OFFLOADS = 1 << 2
	VIRTIO_NET_F_MTU                 = 1 << 3 // Device reports MTU in config
	VIRTIO_NET_F_MAC                 = 1 << 5 // Device has a valid MAC in config
	VIRTIO_NET_F_GUEST_TSO4          = 1 << 7
	VIRTIO_NET_F_GUEST_TSO6          = 1 << 8
	VIRTIO_NET_F_GUEST_ECN           = 1 << 9
	VIRTIO_NET_F_GUEST_UFO           = 1 << 10
	VIRTIO_NET_F_HOST_TSO4           = 1 << 11
	VIRTIO_NET_F_HOST_TSO6           = 1 << 12
	VIRTIO_NET_F_HOST_ECN            = 1 << 13
	VIRTIO_NET_F_HOST_UFO            = 1 << 14
	VIRTIO_NET_F_MRG_RXBUF           = 1 << 15 // Driver can merge receive buffers
	VIRTIO_NET_F_STATUS              = 1 << 16 // Device reports link status in config
	VIRTIO_NET_F_CTRL_VQ             = 1 << 17 // Control virtqueue available
	VIRTIO_NET_F_CTRL_RX             = 1 << 18
	VIRTIO_NET_F_CTRL_VLAN           = 1 << 19
	VIRTIO_NET_F_GUEST_ANNOUNCE      = 1 << 21
	VIRTIO_NET_F_MQ                  = 1 << 22 // Multiqueue support
	VIRTIO_NET_F_CTRL_MAC_ADDR       = 1 << 23
)

// Net feature bits — high feature page (bits 32-63), Handshake's `high` argument.
// Bit position is (N - 32); pair these with virtio.FeatureVersion1 in `high`.
const (
	VIRTIO_NET_F_RSC_EXT      = 1 << (61 - 32)
	VIRTIO_NET_F_STANDBY      = 1 << (62 - 32)
	VIRTIO_NET_F_SPEED_DUPLEX = 1 << (63 - 32)
)

// VirtIONetConfig mirrors the device-specific config region (spec §5.1.4).
// Only leading fields are always valid; later fields require their feature bit.
type VirtIONetConfig struct {
	MAC               [6]uint8 // Valid iff VIRTIO_NET_F_MAC
	Status            uint16   // Valid iff VIRTIO_NET_F_STATUS
	MaxVirtqueuePairs uint16   // Valid iff VIRTIO_NET_F_MQ
	MTU               uint16   // Valid iff VIRTIO_NET_F_MTU
}

// Device-config field offsets (bytes from PCIDevice.DeviceConfigBase).
const (
	netCfgMACOffset               = 0  // MAC[6]
	netCfgStatusOffset            = 6  // uint16
	netCfgMaxVirtqueuePairsOffset = 8  // uint16
	netCfgMTUOffset               = 10 // uint16
)

// VirtIONetConfig.Status bits
const (
	VIRTIO_NET_S_LINK_UP  = 1
	VIRTIO_NET_S_ANNOUNCE = 2
)
