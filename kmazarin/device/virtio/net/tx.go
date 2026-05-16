// tx.go — VirtIO-net transmit path (MAZ-20, MAZ-23).
//
// Step 4 of the MAZ-16 virtio-net chain. Builds a 2-descriptor TX chain
// (VirtIONetHdr + frame payload, both device-readable), submits it on the TX
// Engine, notifies the device, and reclaims the completion by polling the used
// ring. No interrupts — TX completions are reclaimed synchronously by polling
// PopUsed (interrupt wiring is MAZ-26).
//
// The whole TX path is cache-management-free: the VirtIONetHdr lives in a
// Device-nGnRnE sidecar slot and the payload buffer is a Device-nGnRnE driver
// page, so CPU writes are immediately device-visible. The only cacheable memory
// is the queue/descriptor page, and Engine.Submit already cleans descriptors
// itself.
package net

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
)

// TX path constants.
const (
	// txMaxFrameLen is the largest Ethernet frame SendTx accepts: 1514-byte
	// payload (14-byte header + 1500 MTU). The TX payload buffer is one 4KB
	// driver page, so this fits comfortably.
	txMaxFrameLen = 1514

	// txPollMaxIterations bounds the completion poll loop. Each iteration does
	// an MMIO ISR read (a guaranteed VM exit under HVF), so this is a generous
	// cap before declaring a timeout — a healthy device completes a TX in a
	// handful of iterations.
	txPollMaxIterations = 1000000
)

// txInit allocates the single Device-nGnRnE TX payload DMA buffer. The TX
// Engine itself (virtqueue 1) and the sidecar pool are already brought up by
// virtioNetInit (MAZ-19) — txInit only adds the payload buffer. Returns false
// on allocation failure.
func txInit(dev *VirtIONetDevice) bool {
	pa, va := kmem.AllocDriverPage()
	if pa == 0 {
		klog.Errf("[VirtIO Net] ERROR: failed to allocate TX payload buffer\n")
		return false
	}
	dev.TxBufPA = pa
	dev.TxBufVA = va
	return true
}

// SendTx sends a single raw Ethernet frame on the TX virtqueue. It builds a
// 2-descriptor chain — Descs[0] = a zeroed VirtIONetHdr in a sidecar slot,
// Descs[1] = the frame payload copied into the Device-nGnRnE TX buffer — both
// device-readable (Flags = 0, no VIRTQ_DESC_F_WRITE). After Submit + Notify it
// waits for the completion via a bootYieldForIO-style hybrid loop (check
// HasUsed; if not done, ISR-read + WFI). The ISR read forces a VM exit under
// HVF and guards the stale-GIC WFI-deadlock; the WFI sleeps until the next
// interrupt — typically the TX-completion MSI-X that MAZ-26 now delivers,
// but any IRQ (e.g. timer) wakes it.
//
// Pre-MAZ-26 this loop was a tight MMIO-spin with no WFI because net had no
// interrupts to wake one. The WFI is the efficiency win MAZ-26 unlocks.
//
// Synchronous: one in-flight TX at a time, which is all the current callers
// need. Returns true on a reclaimed completion, false on a bad length, an
// exhausted sidecar pool, a submit failure, or a poll timeout.
//
// SendTx satisfies the device.NetDevice interface (MAZ-23).
//
// SendTx is reentrant-safe: a concurrent caller is rejected with false
// (klog'd) via an atomic CAS on d.txInUse. The flag is cleared on every
// exit path including the timeout-leak case.
//
//go:nosplit
func (d *VirtIONetDevice) SendTx(frame []byte) bool {
	// Serialize against concurrent SendTx callers — the shared TX DMA
	// buffer (d.TxBufVA), the TX Engine, and the sidecar pool can only
	// service one in-flight TX at a time. SendTx is //go:nosplit so a
	// sync.Mutex would call morestack; atomic CAS matches the RxPending
	// pattern in interrupt.go. The flag is cleared on every exit path
	// (incl. timeout) so a leaked-sidecar timeout doesn't jam the gate.
	if !atomic.CompareAndSwapUint32(&d.txInUse, 0, 1) {
		klog.Errf("[VirtIO Net] TX: concurrent SendTx rejected\n")
		return false
	}

	n := len(frame)
	if n == 0 || n > txMaxFrameLen {
		klog.Errf("[VirtIO Net] TX: bad frame length\n")
		atomic.StoreUint32(&d.txInUse, 0)
		return false
	}

	// VirtIONetHdr goes in a Device-nGnRnE sidecar slot — zeroed: plain frame,
	// no checksum offload, no GSO. The nGnRnE write is immediately
	// device-visible, so no cache management.
	slot, ok := d.Sidecars.Alloc()
	if !ok {
		klog.Errf("[VirtIO Net] TX: no sidecar slots\n")
		atomic.StoreUint32(&d.txInUse, 0)
		return false
	}
	*(*VirtIONetHdr)(unsafe.Pointer(slot.VA)) = VirtIONetHdr{}

	// Copy the payload into the Device-nGnRnE TX buffer — no cache management
	// (contrast block, which uses a cacheable buffer + CleanDCacheRange).
	dst := (*[txMaxFrameLen]byte)(unsafe.Pointer(d.TxBufVA))
	for i := 0; i < n; i++ {
		dst[i] = frame[i]
	}

	// 2-desc chain: header → payload, both device-readable.
	var chain virtio.DescChain
	chain.Count = 2
	chain.Descs[0] = virtio.DescSpec{
		PA:    uint64(slot.PA),
		Len:   uint32(unsafe.Sizeof(VirtIONetHdr{})),
		Flags: 0,
	}
	chain.Descs[1] = virtio.DescSpec{
		PA:    uint64(d.TxBufPA),
		Len:   uint32(n),
		Flags: 0,
	}

	tag := d.TxEng.Submit(&chain)
	if tag == virtio.InvalidIOTag {
		klog.Errf("[VirtIO Net] TX: submit failed\n")
		d.Sidecars.Release(slot)
		atomic.StoreUint32(&d.txInUse, 0)
		return false
	}

	asm.Dsb()
	d.TxEng.Notify()

	// Wait for the completion via the bootYieldForIO-style hybrid loop:
	// check HasUsed; if not done, ISR-read + WFI. The ISR read forces a VM
	// exit (services QEMU's backend + guards the stale-GIC WFI deadlock
	// block/io_arm64.go documents); the WFI sleeps until the next interrupt
	// — typically the TX-completion MSI-X that MAZ-26 wires.
	for i := 0; i < txPollMaxIterations; i++ {
		asm.DmaRmb()
		if d.TxEng.HasUsed() {
			info := d.TxEng.PopUsed() // frees the descriptor chain
			d.Sidecars.Release(slot)
			if info.Tag != tag {
				klog.Errf("[VirtIO Net] TX: unexpected completion tag\n")
				atomic.StoreUint32(&d.txInUse, 0)
				return false
			}
			atomic.StoreUint32(&d.txInUse, 0)
			return true
		}
		if d.ISRBase != 0 {
			_ = asm.MmioRead8(d.ISRBase)
		}
		asm.Wfi()
	}

	// Timed out — leave the sidecar slot leaked: the device may still own
	// the descriptor chain and could write to it later. Clear txInUse
	// anyway so subsequent callers aren't permanently locked out.
	klog.Errf("[VirtIO Net] TX: completion timed out\n")
	atomic.StoreUint32(&d.txInUse, 0)
	return false
}
