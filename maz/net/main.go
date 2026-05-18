// net is the standalone ethernet-layer shepherd. It owns the VirtIO-net
// device and hosts an L3+ protocol stack as a .maz plugin.
//
// Phase A (MAZ-28 commits 1–4) brought up the raw L2 path: kernel IRQ
// pushes typed CQEs, net.elf re-arms RX descriptors and submits TX
// frames over io_uring, end-to-end ARP round-trip at 3-4µs IRQ→
// shepherd latency.
//
// Phase B step 5 swaps the original two-plugin design (ethernet.maz +
// gvisor.maz) for a single L3+ plugin: gvisor's link/ethernet wrapper
// owns the 14-byte L2 header. Net.elf only owns the 12-byte
// virtio_net_hdr — RX pages are handed to the plugin starting at
// offset 12 (where the eth header lives); on TX the plugin writes the
// eth header + L3 at offset 12, and net.elf zeros virtio_net_hdr at
// page+0 before submission.
package main

import (
	"fmt"
	"os"

	"mazzy/maz/net/host"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/iouring"
	"mazzy/shared/ipc"
)

const (
	// poolPages backs the Allocator. 128 matches the doc's "32 always-
	// armed + 32 client-loan + 64 reserve" split, with watermarks
	// (Soft=32, Hard=96) defined in maz/net/host/allocator.go.
	poolPages = 128

	// rxArmedCount is the number of RX descriptors the Dispatcher
	// pre-arms. Matches the "32 always-armed" share of the pool.
	rxArmedCount = 32

	// netDeviceType is the kernel's IOUringDeviceNet enum value passed
	// to sys.IOUringSetup to claim the virtio-net device.
	netDeviceType = 2

	// fsRing is the IPC ring index reserved for fsclient responses
	// (matches shepherd.elf's convention).
	fsRing = 1

	// pluginHeadroom is the byte count plugins must leave at the start
	// of each page for net.elf's virtio_net_hdr. Matches
	// host.VirtIONetHdrSize; the plugin writes its eth header + L3 + …
	// starting at VABase+pluginHeadroom.
	pluginHeadroom = host.VirtIONetHdrSize
)

func main() {
	fmt.Println("net: up")

	_ = setupFSClient() // fsclient must exist for plugin loads via mazhost.
	alloc := setupAllocator()
	ring, netRingID := setupNetRing()

	dev := host.NewDevice(alloc, [6]uint8{}) // zero → falls back to QEMU's default MAC
	_ = dev // gvisor.maz load slot — Phase B step 5 follow-up.

	dispatcher := host.NewDispatcher(ring, netRingID, rxArmedCount, alloc, nil)
	_ = dispatcher
	// TODO(MAZ-28 Phase B step 5): once gvisor.maz exists, load it here
	// via host.LoadLinkSurfacePlugin(fc, "gvisor", dev, alloc); wire its
	// TxChan into host.RunTxWorkers; PreArm the dispatcher; spin Run.
	// Until then net.elf is a quiet host with no plugin loaded.

	fmt.Println("[net] awaiting L3+ plugin (gvisor.maz not yet loaded)")
	select {}
}

// setupFSClient wires net.elf to the fs shepherd over a dedicated
// uring response ring (per the "FS dedicated response ring" rule —
// ring 0 sharing causes head-of-line deadlock).
func setupFSClient() fsclient.FSClient {
	if err := uring.Setup(fsRing); err != nil {
		fmt.Printf("[net] uring.Setup(%d) failed: %v\n", fsRing, err)
		os.Exit(1)
	}
	fsSID := sys.MustGetShepherdByName("fs")
	fc := fsclient.New(fsSID)
	fc.SetRespRing(uint8(fsRing))

	disp := uring.NewDispatcherWithRing(fsRing)
	disp.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.GetRespCh())
	disp.Start()

	if err := fc.Connect(); err != nil {
		fmt.Printf("[net] fsclient.Connect failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[net] fsclient connected on ring %d (fsSID=%d)\n", fsRing, fsSID)
	mazhost.HostFSClient = fc
	return fc
}

// setupAllocator allocates the contiguous DMA pool. Headroom=12 leaves
// room for net.elf's virtio_net_hdr at the front of every page.
func setupAllocator() *host.Allocator {
	alloc, err := host.NewAllocator(poolPages, pluginHeadroom)
	if err != nil {
		fmt.Printf("[net] host.NewAllocator failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[net] DMA pool: %d pages at 0x%x (headroom=%d)\n",
		alloc.PageCount(), uint64(alloc.PoolBase()), alloc.Headroom())
	return alloc
}

// setupNetRing claims the virtio-net device and returns the io_uring
// ring + numeric ringID for it.
func setupNetRing() (*iouring.IORing, int) {
	ringPage, err := mem.AllocPages(1, mem.PageShared)
	if err != nil {
		fmt.Printf("[net] AllocPages(netRing) failed: %v\n", err)
		os.Exit(1)
	}
	ring := (*iouring.IORing)(ringPage)
	ringID, setupErr := sys.IOUringSetup(ring, netDeviceType)
	if setupErr != nil {
		fmt.Printf("[net] IOUringSetup(net) failed: %v\n", setupErr)
		os.Exit(1)
	}
	fmt.Printf("[net] io_uring created: ringID=%d type=%d\n", ringID, netDeviceType)
	return ring, ringID
}
