// net is the standalone ethernet-layer shepherd. It owns the VirtIO-net
// device and hosts protocol stacks as .maz plugins.
//
// Phase A (MAZ-28 commits 1–4) brought up the raw L2 path: kernel IRQ
// pushes typed CQEs, net.elf re-arms RX descriptors and submits TX
// frames over io_uring, end-to-end ARP round-trip at 3-4µs IRQ→
// shepherd latency.
//
// Phase B step 1 introduced the linksurface package and its two
// boundaries (LinkSurface for L3 plugins, EthFraming for the L2
// plugin). Step 2 implemented the net.elf side (host package).
// Step 3 wrote ethernet.maz. Step 4 (this file's rewrite) loads
// ethernet.maz, wires the Dispatcher + TxWorkers into the io_uring
// ring, replaces the inline ARP frame builder with a TxEnvelope sent
// through the plugin boundary, and switches RX pre-arm to page+6 so
// L3 lands 16-byte-aligned at offset 32 — same page layout the L3
// plugin (gvisor, step 5) will receive.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"mazzy/maz/net/host"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/linksurface"
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
	// (matches shepherd.elf's convention). Ring 0 is kernel-allocated;
	// ring 2 is free.
	fsRing = 1
)

// hwAddr — QEMU's default virtio-net MAC. Used to fill the ARP body's
// SHA field. The ethernet.maz plugin separately uses the same value
// for the outbound eth header's source field. Replace when a
// SysNetReadMAC syscall lands; see maz/net/host/device.go +
// maz/ethernet/framing.go for the matching TODOs.
var hwAddr = [6]uint8{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}

func main() {
	fmt.Println("net: up")

	fc := setupFSClient()
	alloc := setupAllocator()
	ring, netRingID := setupNetRing()

	framingInit, err := host.LoadEthFramingPlugin(fc, "ethernet", alloc)
	if err != nil {
		fmt.Printf("[net] LoadEthFramingPlugin(ethernet) failed: %v\n", err)
		os.Exit(1)
	}
	framing := framingInit.Framing
	fmt.Printf("[net] ethernet.maz loaded; Framing.Headroom=%d Device.Headroom=%d\n",
		framing.Headroom(), alloc.Headroom())

	// Recv channel the (not-yet-loaded) L3 plugin would read from. Until
	// gvisor.maz lands (step 5), a no-op consumer logs + Releases so the
	// pool stays balanced and the ARP test still produces visible output.
	recvChan := make(chan linksurface.RxEnvelope, host.RecvChanBuffer)

	dispatcher := host.NewDispatcher(ring, netRingID, rxArmedCount, alloc, framing, recvChan)
	if err := dispatcher.PreArm(); err != nil {
		fmt.Printf("[net] dispatcher.PreArm failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[net] pre-armed %d RX descriptors\n", rxArmedCount)
	go dispatcher.Run()
	go runRxConsumer(alloc, recvChan)

	// Net.elf-internal TxChan. Step 5 wires the L3 plugin to drive this;
	// for the ARP bring-up, net.elf produces a single TxEnvelope itself
	// and the TxWorker pool reads it like any plugin-produced one.
	txChan := make(chan linksurface.TxEnvelope, 32)
	go host.RunTxWorkers(host.TxWorkers, dispatcher, txChan)

	sendArpProbe(alloc, txChan)

	// Block forever; the Dispatcher / TxWorkers / RxConsumer goroutines
	// own the lifetime from here.
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

// setupAllocator allocates the contiguous DMA pool and reports its
// extent. Headroom=32 matches Device.Headroom (round_up(virtio_net_hdr
// (12) + eth header (14), 16)).
func setupAllocator() *host.Allocator {
	alloc, err := host.NewAllocator(poolPages, 32)
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

// runRxConsumer is the stand-in L3 plugin. Until gvisor.maz lands
// (step 5), this drains RecvChan, logs srcMAC + ethertype + L3 length
// (proves the dispatcher → Framing.Validate → channel path works
// end-to-end), and Releases the page so the pool stays balanced.
func runRxConsumer(alloc *host.Allocator, recvChan <-chan linksurface.RxEnvelope) {
	for env := range recvChan {
		mac := env.SrcMAC
		fmt.Printf("[net] RX l3Len=%d src=%02x:%02x:%02x:%02x:%02x:%02x ethertype=0x%04x\n",
			env.Packet.Len(),
			uint8(mac>>40), uint8(mac>>32), uint8(mac>>24),
			uint8(mac>>16), uint8(mac>>8), uint8(mac),
			env.Ethertype)
		alloc.Release(env.Packet)
	}
}

// sendArpProbe allocates a TX page, writes the 28-byte ARP body at
// VABase+Offset (= page+Headroom = page+32), and queues a TxEnvelope
// on txChan. A TxWorker reads it, calls Framing.AddSendBytes (which
// fills the 14-byte eth header + zeros the 12-byte virtio_net_hdr),
// and submits the TX SQE.
//
// Body content: gratuitous ARP-request for 10.0.2.2 (SLIRP gateway),
// same wire contents as the Phase A inline probe.
func sendArpProbe(alloc *host.Allocator, txChan chan<- linksurface.TxEnvelope) {
	pkt := alloc.AllocTx()
	if pkt == nil {
		fmt.Println("[net] ARP: AllocTx returned nil — pool exhausted")
		return
	}
	arp := unsafe.Slice((*byte)(unsafe.Pointer(pkt.VABase()+uintptr(pkt.Offset()))), 28)
	binary.BigEndian.PutUint16(arp[0:2], 0x0001)  // HTYPE Ethernet
	binary.BigEndian.PutUint16(arp[2:4], 0x0800)  // PTYPE IPv4
	arp[4] = 6                                    // HLEN
	arp[5] = 4                                    // PLEN
	binary.BigEndian.PutUint16(arp[6:8], 0x0001)  // OPER request
	copy(arp[8:14], hwAddr[:])                    // SHA (our MAC)
	arp[14], arp[15], arp[16], arp[17] = 0, 0, 0, 0 // SPA (we don't know our IP)
	for i := 18; i < 24; i++ {
		arp[i] = 0 // THA (asking)
	}
	arp[24], arp[25], arp[26], arp[27] = 10, 0, 2, 2 // TPA = 10.0.2.2

	txChan <- linksurface.TxEnvelope{
		Packet:    pkt,
		Len:       28,
		DstMAC:    0xffffffffffff, // broadcast
		Ethertype: 0x0806,         // ARP
	}
	fmt.Println("[net] ARP probe queued via plugin chain for 10.0.2.2")
}
