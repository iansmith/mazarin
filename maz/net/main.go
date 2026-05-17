// net is the standalone ethernet-layer shepherd. It owns the VirtIO-net
// device and exposes a raw L2 io_uring send/receive surface that higher
// protocol layers (gvisor tcpip, quic-go, future transports) attach to as
// .maz plugins via dependency injection at load time.
//
// MAZ-28 step 2: raw RX path. Allocates a 128-page DMA pool, pre-arms 32
// descriptors, runs a consumer goroutine that drains CQEs the kernel IRQ
// top-half pushes. Per frame, klogs srcMAC + ethertype + IRQ→shepherd
// latency in µs and re-arms the descriptor via IOUringOpNetRearmDesc.
//
// TX path lands in step 3; ARP round-trip + legacy tear-out in step 4.
// Plugin loader + injection API + first .maz protocol plugin: MAZ-27.
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
	// rxPoolPages is the size of net.elf's RX page pool (one DMA page
	// per descriptor slot). 128 pages = 512 KB. Suggested split per
	// docs/net-shepherd.md: 32 armed + 32 client-loan + 64 reserve.
	// Step 2 uses only the armed slice.
	rxPoolPages = 128

	// rxArmedCount is the number of descriptors pre-armed at startup.
	// Bounded by SQ capacity (32) to fit a single batched enter.
	rxArmedCount = 32

	// virtioNetHdrSize is sizeof(virtio_net_hdr) — the device prepends
	// it before the Ethernet frame in each RX buffer.
	virtioNetHdrSize = 12

	// netDeviceType is the io_uring device type for net rings (matches
	// IOUringDeviceNet in kmazarin/kmazarin/iouring.go).
	netDeviceType = 2
)

func main() {
	fmt.Println("net: up")

	// 1. Allocate the RX page pool (contiguous DMA, one page per slot).
	rxPool, err := mem.AllocContiguous(rxPoolPages * 4096)
	if err != nil {
		fmt.Printf("[net] AllocContiguous(rxPool) failed: %v\n", err)
		os.Exit(1)
	}
	rxPoolVA := rxPool.Addr
	fmt.Printf("[net] RX pool: %d pages at 0x%x\n", rxPoolPages, uint64(rxPoolVA))

	// 2. io_uring ring page + setup with deviceType=net.
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

	// 3. Pre-arm rxArmedCount descriptors (slot N gets pool page N).
	for tag := uint32(0); tag < rxArmedCount; tag++ {
		writeRearmSQE(ring, tag, rxPoolVA+uintptr(tag)*4096)
	}
	atomic.StoreUint32(&ring.SQTail, rxArmedCount)
	n, err := sys.IOUringEnter(ringID, rxArmedCount, 0, 0)
	if err != nil {
		fmt.Printf("[net] pre-arm IOUringEnter failed: %v (n=%d)\n", err, n)
		os.Exit(1)
	}
	fmt.Printf("[net] pre-armed %d RX descriptors\n", rxArmedCount)

	// 4. RX consumer — blocks on CQEs, decodes per frame, re-arms.
	runRxConsumer(ring, ringID, rxPoolVA)
}

// writeRearmSQE writes an IOUringOpNetRearmDesc SQE at the current
// SQTail slot for the given descriptor tag and page VA. The caller is
// responsible for advancing SQTail and calling IOUringEnter.
func writeRearmSQE(ring *iouring.IORing, tag uint32, pageVA uintptr) {
	sqTail := atomic.LoadUint32(&ring.SQTail)
	idx := sqTail & iouring.SQMask
	ring.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpNetRearmDesc,
		FD:       int32(tag), // descIdx
		Addr:     uint64(pageVA),
		UserData: uint64(tag),
	}
	atomic.StoreUint32(&ring.SQTail, sqTail+1)
}

// runRxConsumer is the RX dispatch loop. Blocks on CQEs the kernel
// IRQ top-half pushes; per frame klogs the src MAC, ethertype, and
// IRQ→shepherd latency, then re-arms the descriptor.
//
// Never returns under normal operation. On unexpected IOUringEnter
// failures it logs and continues (the kernel can repost completions).
func runRxConsumer(ring *iouring.IORing, ringID int, rxPoolVA uintptr) {
	for {
		// Wait for ≥1 CQE. P-holding RawSyscall — the 10ms safety
		// timeout (kernel-side) bounds the wait so the loop also
		// services bookkeeping/heartbeat in the future.
		_, err := sys.IOUringEnter(ringID, 0, 1, 0)
		if err != nil {
			// Spurious wake or timeout — drain whatever is there
			// and loop again. Avoid tight error-busy-loop.
			time.Sleep(1 * time.Millisecond)
		}

		cqHead := atomic.LoadUint32(&ring.CQHead)
		cqTail := atomic.LoadUint32(&ring.CQTail)
		rearms := uint32(0)
		for cqHead != cqTail {
			cqe := ring.CQEntries[cqHead&iouring.CQMask]
			tag := uint16(cqe.UserData)
			usedLen := int(cqe.Res)
			cqHead++

			latencyUs := sys.NetReadRxLatencyUs(tag)
			frameVA := rxPoolVA + uintptr(tag)*4096
			logFrame(frameVA, tag, usedLen, latencyUs)

			// Re-arm the same slot with the same page VA — slot
			// pinned, page-pinned.
			writeRearmSQE(ring, uint32(tag), frameVA)
			rearms++
		}
		atomic.StoreUint32(&ring.CQHead, cqHead)

		if rearms > 0 {
			// Push the re-arms; don't wait for completion.
			_, _ = sys.IOUringEnter(ringID, rearms, 0, 0)
		}
	}
}

// logFrame parses the [virtio_net_hdr][Ethernet] in the RX buffer and
// prints src MAC + ethertype + latency. usedLen is the device-written
// byte count (includes the virtio_net_hdr prefix).
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
