package main

// [MAZ-179 probe — NOT FOR MERGE, tier 12] DMA-descriptor liveness scan,
// called (via //go:linkname from ksyscall) at every DMA-clump BuddyFree.
//
// Rationale: the whole MAZ-15/179 tripwire suite (MAP_LEAK, MAP_STALE,
// poison, doubleBook) watches PTEs and page descriptors. Virtio descriptors
// are neither: munmapClump unmaps the PTEs FIRST (MapCount drops to 0,
// every PTE check satisfied) and then frees the frames — while a descriptor
// still posted in a device ring keeps a live PA the device will DMA-write.
// QEMU's device backends run on host iothreads, so on this single-vCPU
// guest they are the only truly asynchronous writers — the profile the
// tier-9b µs-scale mspan rewrite demands. This scan is the missing
// descriptor-liveness sibling of buddyMapLeakCheck.

import (
	"unsafe"

	"mazzy/kmazarin/device/virtio"
	"mazzy/shared/constants"
)

// DMAScanRange reports how many live DMA references into [startPA, endPA)
// exist at this instant: block in-flight async slots (device-write reads
// only — dataKernelVA is zeroed for writes at submit time) and posted
// descriptors in the net RX/TX virtqueues. Detection only; no behavior
// change. Nosplit: called from the nosplit munmapClump free path.
//
//go:nosplit
func DMAScanRange(startPA, endPA uintptr) (blkHits, netHits int32) {
	// Block: a non-zero dataKernelVA marks an in-flight READ (device-write)
	// whose completion has not yet cleared the slot.
	for i := 0; i < len(blockAsyncSlots); i++ {
		kva := blockAsyncSlots[i].dataKernelVA
		if kva == 0 {
			continue
		}
		pa := kva - constants.KernelVAOffset
		if pa >= startPA && pa < endPA {
			blkHits++
		}
	}
	if netRxEnginePtr != 0 {
		netHits += vqScanPostedRange((*virtio.Engine)(unsafe.Pointer(netRxEnginePtr)), startPA, endPA)
	}
	if netTxEnginePtr != 0 {
		netHits += vqScanPostedRange((*virtio.Engine)(unsafe.Pointer(netTxEnginePtr)), startPA, endPA)
	}
	return blkHits, netHits
}

// vqScanPostedRange counts descriptors NOT on the virtqueue free list whose
// Addr falls in [startPA, endPA). Free-list membership is derived by walking
// the Next chain from FreeHead for NumFree steps (the same encoding
// VirtqueueAllocDesc consumes), so stale Addr values in recycled free slots
// are not counted. Bounded: QueueSize <= 512.
//
//go:nosplit
func vqScanPostedRange(eng *virtio.Engine, startPA, endPA uintptr) int32 {
	vq := &eng.VQ
	qs := int(vq.QueueSize)
	if qs == 0 || qs > 512 || vq.DescTable == nil {
		return 0
	}
	var freeMask [8]uint64 // 512 bits
	idx := vq.FreeHead
	for n := 0; n < int(vq.NumFree) && n < qs; n++ {
		if int(idx) >= qs {
			break // corrupt free chain — count nothing beyond it
		}
		freeMask[idx>>6] |= 1 << (idx & 63)
		d := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(idx)*unsafe.Sizeof(virtio.VirtQDesc{})))
		idx = d.Next
	}
	var hits int32
	for i := 0; i < qs; i++ {
		if freeMask[i>>6]&(1<<(uint(i)&63)) != 0 {
			continue
		}
		d := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(i)*unsafe.Sizeof(virtio.VirtQDesc{})))
		pa := uintptr(d.Addr)
		if pa >= startPA && pa < endPA {
			hits++
		}
	}
	return hits
}
