// net is the standalone ethernet-layer shepherd. It owns the VirtIO-net
// device and exposes a raw L2 io_uring send/receive surface that higher
// protocol layers (gvisor tcpip, quic-go, future transports) attach to as
// .maz plugins via dependency injection at load time.
//
// MAZ-28 step 2 added RX. Step 3 adds TX (IOUringOpNetSubmitTx). Together
// they ship the raw L2 path: net.elf can pre-arm 32 RX descriptors via
// IOUringOpNetRearmDesc and send raw frames via IOUringOpNetSubmitTx; the
// kernel IRQ top-half drains both rings and pushes typed CQEs back.
//
// Step 4 closes the loop with an ARP round-trip and removes the MAZ-26
// stopgap structures (RxPending, DrainRxFromBottomHalf, txInUse CAS, the
// 2-bufs-per-page RX pool). MAZ-27 adds the .maz plugin loader and the
// first protocol plugin (gvisor tcpip LinkEndpoint).
package main

import (
	"encoding/binary"
	"fmt"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/shared/iouring"
	"os"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	rxPoolPages  = 128
	rxArmedCount = 32

	// txPoolPages — net.elf's transmit page pool. Step 3 uses a tiny
	// pool (4 pages) because the only sender is the startup ARP probe;
	// step 4's ARP round-trip uses one slot. The plugin loader (MAZ-27)
	// will turn this into a proper allocator.
	txPoolPages = 4

	virtioNetHdrSize = 12
	netDeviceType    = 2 // IOUringDeviceNet
)

// QEMU's default MAC for virtio-net devices. Used as the source MAC for
// step 3's startup ARP probe. A proper "read my MAC" syscall is a small
// piece of MAZ-27 plumbing — the kernel knows it (VirtIONetDevice.HWAddr)
// but exposing it cleanly is out of scope here.
var hwAddr = [6]uint8{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}

func main() {
	fmt.Println("net: up")

	rxPool, err := mem.AllocContiguous(rxPoolPages * 4096)
	if err != nil {
		fmt.Printf("[net] AllocContiguous(rxPool) failed: %v\n", err)
		os.Exit(1)
	}
	rxPoolVA := rxPool.Addr
	fmt.Printf("[net] RX pool: %d pages at 0x%x\n", rxPoolPages, uint64(rxPoolVA))

	txPool, err := mem.AllocContiguous(txPoolPages * 4096)
	if err != nil {
		fmt.Printf("[net] AllocContiguous(txPool) failed: %v\n", err)
		os.Exit(1)
	}
	txPoolVA := txPool.Addr
	fmt.Printf("[net] TX pool: %d pages at 0x%x\n", txPoolPages, uint64(txPoolVA))

	ringPage, ringErr := mem.AllocPages(1, mem.PageShared)
	if ringErr != nil {
		fmt.Printf("[net] AllocPages(ring) failed: %v\n", ringErr)
		os.Exit(1)
	}
	ring := (*iouring.IORing)(ringPage)
	ringID, setupErr := sys.IOUringSetup(ring, netDeviceType)
	if setupErr != nil {
		fmt.Printf("[net] IOUringSetup failed: %v\n", setupErr)
		os.Exit(1)
	}
	fmt.Printf("[net] io_uring created: ringID=%d type=%d\n", ringID, netDeviceType)

	// Pre-arm RX descriptors (slot N gets pool page N).
	for tag := uint32(0); tag < rxArmedCount; tag++ {
		writeRearmSQE(ring, tag, rxPoolVA+uintptr(tag)*4096)
	}
	atomic.StoreUint32(&ring.SQTail, rxArmedCount)
	if _, err := sys.IOUringEnter(ringID, rxArmedCount, 0, 0); err != nil {
		fmt.Printf("[net] pre-arm IOUringEnter failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[net] pre-armed %d RX descriptors\n", rxArmedCount)

	// Send a startup ARP probe (gratuitous ARP for 10.0.2.2 — the SLIRP
	// gateway). With step 3's TX path live, this exercises the SubmitTx
	// SQE and should appear in QEMU's pcap. If SLIRP replies, the RX
	// path delivers the reply CQE.
	sendArpProbe(ring, ringID, txPoolVA, 1)

	runConsumer(ring, ringID, rxPoolVA)
}

// writeRearmSQE writes an IOUringOpNetRearmDesc SQE at SQTail for the
// given descriptor tag and page VA. Caller advances SQTail + enters.
func writeRearmSQE(ring *iouring.IORing, tag uint32, pageVA uintptr) {
	sqTail := atomic.LoadUint32(&ring.SQTail)
	idx := sqTail & iouring.SQMask
	ring.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpNetRearmDesc,
		FD:       int32(tag),
		Addr:     uint64(pageVA),
		UserData: iouring.NetEncodeRxUserData(uint16(tag)),
	}
	atomic.StoreUint32(&ring.SQTail, sqTail+1)
}

// writeTxSQE writes an IOUringOpNetSubmitTx SQE at SQTail for a single
// frame at (pageVA, offset, length). txTag is echoed in the completion.
func writeTxSQE(ring *iouring.IORing, pageVA, offset uintptr, length uint32, txTag uint16) {
	sqTail := atomic.LoadUint32(&ring.SQTail)
	idx := sqTail & iouring.SQMask
	ring.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpNetSubmitTx,
		Addr:     uint64(pageVA),
		Off:      uint64(offset),
		Len:      length,
		UserData: iouring.NetEncodeTxUserData(txTag),
	}
	atomic.StoreUint32(&ring.SQTail, sqTail+1)
}

// sendArpProbe writes an ARP request for 10.0.2.2 (SLIRP gateway) into
// the first TX-pool page and submits it. txTag identifies the send in
// the completion CQE. Returns once the SQE is queued (does NOT wait
// for the TX completion — the RX consumer loop drains both rings).
//
// VirtIO-net spec requires a 12-byte virtio_net_hdr at the start of
// each TX buffer. We zero it (plain frame, no checksum offload, no
// GSO) and place the Ethernet frame at offset 12. The submit length
// covers both the header and the frame.
func sendArpProbe(ring *iouring.IORing, ringID int, txPoolVA uintptr, txTag uint16) {
	const arpLen = 42                                // 14 (Ethernet) + 28 (ARP)
	const totalLen = virtioNetHdrSize + arpLen       // 12 + 42 = 54
	pageVA := txPoolVA                               // first TX page
	buf := unsafe.Slice((*byte)(unsafe.Pointer(pageVA)), totalLen)

	// VirtIONetHdr: 12 zero bytes (flags=0, gso=NONE, hdr_len=0, ...).
	for i := 0; i < virtioNetHdrSize; i++ {
		buf[i] = 0
	}
	frame := buf[virtioNetHdrSize:]

	// Ethernet header
	frame[0], frame[1], frame[2], frame[3], frame[4], frame[5] = 0xff, 0xff, 0xff, 0xff, 0xff, 0xff
	copy(frame[6:12], hwAddr[:])
	binary.BigEndian.PutUint16(frame[12:14], 0x0806) // ARP

	// ARP body
	binary.BigEndian.PutUint16(frame[14:16], 0x0001) // HTYPE Ethernet
	binary.BigEndian.PutUint16(frame[16:18], 0x0800) // PTYPE IPv4
	frame[18] = 6                                    // HLEN
	frame[19] = 4                                    // PLEN
	binary.BigEndian.PutUint16(frame[20:22], 0x0001) // OPER request
	copy(frame[22:28], hwAddr[:])                    // SHA = our MAC
	frame[28], frame[29], frame[30], frame[31] = 0, 0, 0, 0
	frame[32], frame[33], frame[34], frame[35], frame[36], frame[37] = 0, 0, 0, 0, 0, 0
	frame[38], frame[39], frame[40], frame[41] = 10, 0, 2, 2 // TPA = SLIRP gw

	writeTxSQE(ring, pageVA, 0, totalLen, txTag)
	if _, err := sys.IOUringEnter(ringID, 1, 0, 0); err != nil {
		fmt.Printf("[net] ARP submit IOUringEnter failed: %v\n", err)
		return
	}
	fmt.Printf("[net] ARP probe submitted (txTag=%d) for 10.0.2.2\n", txTag)
}

// runConsumer is the dispatch loop. Blocks on CQEs, dispatches RX vs TX
// by the encoding flag, decodes RX frames, re-arms RX descriptors, and
// logs TX completions.
func runConsumer(ring *iouring.IORing, ringID int, rxPoolVA uintptr) {
	for {
		if _, err := sys.IOUringEnter(ringID, 0, 1, 0); err != nil {
			time.Sleep(1 * time.Millisecond)
		}

		cqHead := atomic.LoadUint32(&ring.CQHead)
		cqTail := atomic.LoadUint32(&ring.CQTail)
		rearms := uint32(0)
		for cqHead != cqTail {
			cqe := ring.CQEntries[cqHead&iouring.CQMask]
			ud := cqe.UserData
			cqHead++

			if iouring.NetIsTxCQE(ud) {
				txTag := iouring.NetDecodeTag(ud)
				fmt.Printf("[net] TX done txTag=%d\n", txTag)
				continue
			}

			tag := iouring.NetDecodeTag(ud)
			usedLen := int(cqe.Res)
			latencyUs := sys.NetReadRxLatencyUs(tag)
			frameVA := rxPoolVA + uintptr(tag)*4096
			logFrame(frameVA, tag, usedLen, latencyUs)

			// Re-arm the same slot.
			writeRearmSQE(ring, uint32(tag), frameVA)
			rearms++
		}
		atomic.StoreUint32(&ring.CQHead, cqHead)

		if rearms > 0 {
			_, _ = sys.IOUringEnter(ringID, rearms, 0, 0)
		}
	}
}

// logFrame parses the [virtio_net_hdr][Ethernet] in the RX buffer and
// prints src MAC + ethertype + latency.
func logFrame(frameVA uintptr, tag uint16, usedLen int, latencyUs uint32) {
	if usedLen < virtioNetHdrSize+14 {
		fmt.Printf("[net] RX runt tag=%d len=%d (skipping decode)\n", tag, usedLen)
		return
	}
	ethBase := unsafe.Add(unsafe.Pointer(frameVA), virtioNetHdrSize)
	eth := unsafe.Slice((*byte)(ethBase), usedLen-virtioNetHdrSize)
	srcMAC := eth[6:12]
	ethertype := binary.BigEndian.Uint16(eth[12:14])
	fmt.Printf("[net] RX tag=%d len=%d src=%02x:%02x:%02x:%02x:%02x:%02x ethertype=0x%04x latency=%dµs\n",
		tag, usedLen,
		srcMAC[0], srcMAC[1], srcMAC[2], srcMAC[3], srcMAC[4], srcMAC[5],
		ethertype, latencyUs)
}
