// rx.go — VirtIO-net receive path (MAZ-22).
//
// Step 3 of the MAZ-16 virtio-net chain. Unlike the block driver's
// request/response model, RX buffers are *pre-posted*: rxInit allocates a pool
// of device-writable buffers and submits every one to the RX Engine before any
// traffic arrives. The device fills a buffer per received frame and posts it to
// the used ring; rxDrain pops completions, peeks per-frame metadata, and
// re-posts the buffer so the queue never starves.
//
// rxDrain is //go:nosplit and allocation-free — written so MAZ-26 can call it
// from the IRQ top-half (NonTimerIRQTopHalf) unchanged. MAZ-22 itself has no
// interrupt wiring; it exercises rxDrain by polling.
//
// Cache discipline: RX buffers are Device-nGnRnE driver pages, so the device's
// writes are immediately CPU-visible — no InvalidateDCacheRange, just an
// asm.DmaRmb() before reading a completed buffer (same as the TX path).
package net

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
)

// RX path constants.
const (
	// netRxBufCount is the number of pre-posted RX buffers. Mirrors input's
	// NumEventBuffers and gives burst headroom; the RX virtqueue is 128 entries
	// (netQueueSize) so 32 in-flight buffers leaves ample slack.
	netRxBufCount = 32

	// netRxBufSize is the byte size of one RX buffer. It must fit a 12-byte
	// VirtIONetHdr plus a full 1514-byte Ethernet frame (= 1526); 2048 gives
	// headroom and packs exactly 2 buffers into a 4KB AllocDriverPage page.
	netRxBufSize = 2048

	// netRxBufsPerPage is how many netRxBufSize buffers fit in one 4KB driver
	// page. netRxBufCount must be a multiple of this.
	netRxBufsPerPage = 4096 / netRxBufSize
)

// rxInit allocates the RX buffer pool and pre-posts every buffer to the RX
// Engine. Buffers are Device-nGnRnE driver pages (kmem.AllocDriverPage), 2 per
// page; each is posted as a single device-writable descriptor chain. Submit
// returns the head descriptor index as the IOTag — recorded in rxInFlight so
// rxDrain can recover which buffer a completion belongs to. Returns false on
// any allocation or submit failure.
func rxInit(dev *VirtIONetDevice) bool {
	pages := netRxBufCount / netRxBufsPerPage
	for p := 0; p < pages; p++ {
		pa, va := kmem.AllocDriverPage()
		if pa == 0 {
			klog.Errf("[VirtIO Net] ERROR: failed to allocate RX buffer page\n")
			return false
		}
		for b := 0; b < netRxBufsPerPage; b++ {
			i := p*netRxBufsPerPage + b
			dev.RxBufPA[i] = pa + uintptr(b)*netRxBufSize
			dev.RxBufVA[i] = va + uintptr(b)*netRxBufSize
		}
	}

	// Pre-post every buffer as a 1-desc device-writable chain.
	for i := 0; i < netRxBufCount; i++ {
		var chain virtio.DescChain
		chain.Count = 1
		chain.Descs[0] = virtio.DescSpec{
			PA:    uint64(dev.RxBufPA[i]),
			Len:   netRxBufSize,
			Flags: virtio.VIRTQ_DESC_F_WRITE,
		}
		tag := dev.RxEng.Submit(&chain)
		if tag == virtio.InvalidIOTag {
			klog.Errf("[VirtIO Net] ERROR: failed to pre-post RX buffer\n")
			return false
		}
		dev.rxInFlight[tag] = uint16(i)
	}

	// Tell the device buffers are available.
	dev.RxEng.Notify()
	return true
}

// rxDrain drains the RX used ring: for every completed buffer it recovers the
// buffer index via rxInFlight, peeks the source MAC and length into the device
// struct (a bring-up affordance — a real consumer hands the whole frame up
// instead), bumps the atomic rxCount, and re-posts the same buffer so the queue
// stays replenished. Returns the number of frames drained.
//
// //go:nosplit and allocation-free: MAZ-26 calls this from the IRQ top-half
// unchanged. No formatted logging (fmt.Sprintf is not nosplit-safe) — plain
// string klog.Errf only. The caller (polling wrapper or MAZ-26's top-half) is
// responsible for the ISR read that forces the VM exit; rxDrain does not touch
// the ISR register itself.
//
//go:nosplit
func rxDrain(dev *VirtIONetDevice) int {
	n := 0
	for dev.RxEng.HasUsed() {
		info := dev.RxEng.PopUsed() // frees the descriptor chain
		if info.Tag == virtio.InvalidIOTag {
			break
		}
		bufIdx := dev.rxInFlight[info.Tag]
		if bufIdx >= netRxBufCount {
			klog.Errf("[VirtIO Net] RX: bad in-flight buffer index\n")
			break
		}
		asm.DmaRmb()

		// The buffer holds [VirtIONetHdr (12B)][Ethernet frame]. The Ethernet
		// frame starts at offset sizeof(VirtIONetHdr); its dst MAC is bytes
		// [0:6] and src MAC is bytes [6:12]. Peek the src MAC + total UsedLen
		// for the bring-up logger. (Post-MAZ-26: a real net consumer takes the
		// whole frame instead of this peek.)
		//
		// RX buffers are Device-nGnRnE — unaligned multi-byte loads fault, so
		// the src MAC is copied one byte at a time.
		srcMACBase := dev.RxBufVA[bufIdx] + unsafe.Sizeof(VirtIONetHdr{}) + 6
		for k := uintptr(0); k < 6; k++ {
			dev.rxLastSrcMAC[k] = *(*uint8)(unsafe.Pointer(srcMACBase + k))
		}
		dev.rxLastLen = info.UsedLen
		atomic.AddUint32(&dev.rxCount, 1)

		// Re-post the same buffer (fresh 1-desc device-writable chain) so the
		// RX queue never starves.
		var chain virtio.DescChain
		chain.Count = 1
		chain.Descs[0] = virtio.DescSpec{
			PA:    uint64(dev.RxBufPA[bufIdx]),
			Len:   netRxBufSize,
			Flags: virtio.VIRTQ_DESC_F_WRITE,
		}
		if tag := dev.RxEng.Submit(&chain); tag != virtio.InvalidIOTag {
			dev.rxInFlight[tag] = bufIdx
		} else {
			klog.Errf("[VirtIO Net] RX: failed to re-post buffer\n")
		}
		n++
	}
	if n > 0 {
		dev.RxEng.Notify()
	}
	return n
}
