// init.go — VirtIO-net device discovery and bring-up (MAZ-19).
//
// Mirrors the block driver's discovery/handshake path (block/block.go) — the
// only structural difference is two Engine.Init calls (RX = virtqueue 0,
// TX = virtqueue 1) instead of one. Bring-up only: no RX/TX traffic, no
// interrupt wiring (MAZ-21 wires interrupts; MAZ-20/22 add TX/RX), no
// device-manager registration (MAZ-23).
package net

import (
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
)

// Bring-up parameters.
const (
	// netQueueSize is the entry count for both the RX and TX virtqueues. A
	// 128-entry queue fits one 4KB DMA page (desc 2048 + avail 262 + used 1030
	// = 3340 bytes); RX and TX therefore get a page each. Mirrors the block driver.
	netQueueSize = 128

	// netSidecarSlotSize is the per-slot byte size of the VirtIONetHdr sidecar
	// pool. VirtIONetHdr is 12 bytes; 16-byte slots keep each slot 4-byte
	// aligned with modest headroom. The pool is initialized here but unused
	// until the TX/RX paths land (MAZ-20/22).
	netSidecarSlotSize = 16

	// netMMIOBase is the fallback PCI MMIO window for BAR reprogramming, used
	// by FindAndMapBARs only if QEMU left a BAR unassigned or above 4GB (rare
	// on q35/virt). Offset 0x500000 keeps it clear of gpu (+0), input
	// (+0x80000 and +0x400000+), block (+0x200000) and rng (+0x300000).
	netMMIOBase = pci.PCI_MMIO_BASE + 0x500000
)

// virtioNetDevice is the global VirtIO network device instance.
var virtioNetDevice VirtIONetDevice

// Init discovers the VirtIO network device on the PCI bus and brings it up:
// PCI BAR mapping, feature handshake, RX + TX queue setup, sidecar pool, and
// DRIVER_OK. Bring-up only — no RX/TX traffic, no interrupt wiring (MAZ-21),
// no device-manager registration (MAZ-23). Returns true on success, false if
// no device was found or initialization failed.
func Init() bool {
	dev := &virtioNetDevice

	if !findVirtIONet(dev) {
		klog.Errf("[VirtIO Net] No network device found\n")
		return false
	}

	if !virtioNetInit(dev) {
		klog.Errf("[VirtIO Net] device initialization failed\n")
		return false
	}

	if !virtioNetReadConfig(dev) {
		klog.Errf("[VirtIO Net] failed to read device config\n")
		return false
	}

	klog.Logf("[VirtIO Net] init OK: MAC=%02x:%02x:%02x:%02x:%02x:%02x status=%#x\n",
		uint64(dev.MAC[0]), uint64(dev.MAC[1]), uint64(dev.MAC[2]),
		uint64(dev.MAC[3]), uint64(dev.MAC[4]), uint64(dev.MAC[5]),
		uint64(dev.Status))
	return true
}

// findVirtIONet scans PCI bus 0 for a VirtIO network device and, on a match,
// maps its BARs into the embedded PCIDevice. Mirrors block's findVirtIOBlock.
func findVirtIONet(dev *VirtIONetDevice) bool {
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			fullReg0 := pci.ConfigRead32(bus, slot, 0, pci.PCI_VENDOR_ID)
			vendorID0 := fullReg0 & 0xFFFF
			if vendorID0 == 0xFFFF || vendorID0 == 0 {
				continue
			}

			headerType := pci.ConfigRead32(bus, slot, 0, 0x0C)
			isMultiFunc := (headerType>>16)&0x80 != 0
			maxFunc := uint8(1)
			if isMultiFunc {
				maxFunc = 8
			}

			for funcNum := uint8(0); funcNum < maxFunc; funcNum++ {
				var fullReg uint32
				if funcNum == 0 {
					fullReg = fullReg0
				} else {
					fullReg = pci.ConfigRead32(bus, slot, funcNum, pci.PCI_VENDOR_ID)
				}
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				if vendorID == pci.VIRTIO_VENDOR_ID &&
					(deviceID == VIRTIO_NET_DEVICE_ID_LEGACY || deviceID == VIRTIO_NET_DEVICE_ID_MODERN) {

					if !dev.FindAndMapBARs(bus, slot, funcNum, netMMIOBase) {
						klog.Errf("[VirtIO Net] ERROR: failed to find/map BARs\n")
						return false
					}
					return true
				}
			}
		}
	}
	return false
}

// virtioNetInit runs the VirtIO handshake and brings up both data queues.
// Mirrors block's virtioBlockInit, with two Engine.Init calls (RX = virtqueue
// 0, TX = virtqueue 1) instead of one. MSI-X is left unconfigured
// (MSIXNoVector) — interrupt wiring is MAZ-21.
//
//go:nosplit
func virtioNetInit(dev *VirtIONetDevice) bool {
	// Feature negotiation. QEMU's virtio-net offers VIRTIO_NET_F_MAC and
	// VIRTIO_NET_F_STATUS by default, which makes the device-config MAC and
	// status reads in virtioNetReadConfig spec-valid. FeatureVersion1 is the
	// high feature page (bit 32).
	if !dev.Handshake(VIRTIO_NET_F_MAC|VIRTIO_NET_F_STATUS, virtio.FeatureVersion1) {
		klog.Errf("[VirtIO Net] ERROR: device rejected features\n")
		return false
	}

	// No MSI-X yet — bring-up only. MAZ-21 wires interrupts.
	dev.SetMSIXConfig(virtio.MSIXNoVector)

	// RX queue (virtqueue 0) on its own DMA page.
	rxPagePA := kmem.AllocKernelFrame()
	if rxPagePA == 0 {
		klog.Errf("[VirtIO Net] ERROR: failed to allocate RX queue DMA page\n")
		return false
	}
	rxPageVA := rxPagePA + constants.KernelMMIOOffset
	if !dev.RxEng.Init(&dev.PCIDevice, 0, netQueueSize, rxPagePA, rxPageVA, 0, virtio.MSIXNoVector) {
		klog.Errf("[VirtIO Net] ERROR: failed to init RX engine\n")
		return false
	}

	// TX queue (virtqueue 1) on its own DMA page.
	txPagePA := kmem.AllocKernelFrame()
	if txPagePA == 0 {
		klog.Errf("[VirtIO Net] ERROR: failed to allocate TX queue DMA page\n")
		return false
	}
	txPageVA := txPagePA + constants.KernelMMIOOffset
	if !dev.TxEng.Init(&dev.PCIDevice, 1, netQueueSize, txPagePA, txPageVA, 0, virtio.MSIXNoVector) {
		klog.Errf("[VirtIO Net] ERROR: failed to init TX engine\n")
		return false
	}

	// Sidecar pool for per-packet VirtIONetHdr slots (unused until MAZ-20/22).
	if !dev.Sidecars.Init(netSidecarSlotSize) {
		klog.Errf("[VirtIO Net] ERROR: failed to init sidecar pool\n")
		return false
	}

	// Complete the handshake.
	dev.SetDriverOK()

	if dev.CheckFailed() {
		klog.Errf("[VirtIO Net] ERROR: device reported FAILED\n")
		return false
	}

	return true
}

// virtioNetReadConfig reads the MAC address and link status from the device's
// config space (mapped by FindAndMapBARs). PCIDevice exposes only 32- and
// 64-bit config readers, so this reads aligned 32-bit words at offsets 0, 4
// and 8 and extracts the sub-fields with shifts. Spec §5.1.4 layout:
// mac[6] @ 0, status @ 6, max_virtqueue_pairs @ 8, mtu @ 10.
//
//go:nosplit
func virtioNetReadConfig(dev *VirtIONetDevice) bool {
	if dev.DeviceConfigBase == 0 {
		klog.Errf("[VirtIO Net] ERROR: no device config BAR mapped\n")
		return false
	}

	reg0 := dev.ReadDeviceConfig32(netCfgMACOffset)              // bytes [0..3]  = MAC[0..3]
	reg4 := dev.ReadDeviceConfig32(netCfgMACOffset + 4)          // bytes [4..7]  = MAC[4..5] + Status
	reg8 := dev.ReadDeviceConfig32(netCfgMaxVirtqueuePairsOffset) // bytes [8..11] = MaxVQPairs + MTU

	dev.MAC[0] = uint8(reg0)
	dev.MAC[1] = uint8(reg0 >> 8)
	dev.MAC[2] = uint8(reg0 >> 16)
	dev.MAC[3] = uint8(reg0 >> 24)
	dev.MAC[4] = uint8(reg4)
	dev.MAC[5] = uint8(reg4 >> 8)
	dev.Status = uint16(reg4 >> 16)      // status @ offset 6
	dev.MaxVirtqueuePairs = uint16(reg8) // max_virtqueue_pairs @ offset 8
	dev.MTU = uint16(reg8 >> 16)         // mtu @ offset 10

	return true
}
