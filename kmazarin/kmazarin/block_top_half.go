// block_top_half.go — Block device IRQ top-half (factored out of bottom_half.go).
//
// Factored out of bottom_half.go for the same reason netTopHalf was: keeping the
// block branch's locals (eng, drained, tag, meta, ioRing, clump, …) in their own
// function prevents them from inflating NonTimerIRQTopHalf's nosplit frame to the
// combined size of all branches.
//
// IRQ hot path (hardware → shepherd):
//   hardware VirtIO block IRQ (MSI-X SPI via GICv2m)
//   → assembly irq_exception_handler (exceptions_arm64.s):
//       reads GICC_IAR (GIC-level ACK, latches IRQ number)
//       switches to kmazarin g0
//   → NonTimerIRQTopHalf (bottom_half.go): dispatches here
//   → blockTopHalf() [this file]:
//       reads VirtIO ISR via MMIO (device-level ACK, deasserts PCI INTx)
//       DMA read barrier
//       async: drains VirtQ used ring, writes CQEs to shared io_uring page
//       sync:  sets *blockIOComplete for WFI loop (early boot only — see below)
//   → WakeIOUringFromIRQ: wakes fs shepherd thread blocked on IOUringEnter
//   → assembly: writes GICC_EOIR (GIC EOI) after NonTimerIRQTopHalf returns
//
// The net device uses the same pattern in net_top_half.go; see that file
// for the parallel structure.
//
// Sync mode survival rationale:
//   blockAsyncMode=0 (sync) is retained for the kernel's own early-boot block
//   I/O, which runs before the fs shepherd process exists.  The kernel uses
//   blockReadBatch / doBlockIO with a WFI spin loop to load the initial ramdisk
//   and shepherd ELF images.  Once SetBlockAsync is called during fs shepherd
//   startup, blockAsyncMode=1 permanently and the sync path is dead.  It must
//   stay in the binary because the kernel build has no conditional compilation
//   and the early-boot path cannot use io_uring (no shepherd ring registered).

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/hid"
	"mazzy/shared/iouring"
	"sync/atomic"
	"unsafe"
)

//go:nosplit
//go:noinline
func blockTopHalf() {
	atomic.AddUint32(&dbgBlockIRQCount, 1)
	if blockISRBase != 0 {
		_ = asm.MmioRead8(blockISRBase) // Acknowledge interrupt (deasserts INTx)
	}
	// DMA read barrier: ensure the device's used ring DMA writes are
	// visible to this CPU before we access used ring entries or signal
	// IOComplete. Under HVF the VirtIO backend runs on a separate host
	// thread; without this barrier the CPU may see stale used ring data.
	asm.DmaRmb()

	if atomic.LoadUint32(&blockAsyncMode) != 0 && blockEnginePtr != 0 {
		// Async mode: drain engine used ring, push completion events
		eng := (*virtio.Engine)(unsafe.Pointer(blockEnginePtr))
		drained := uint32(0)
		// Hoist ring lookup: stable for the entire batch (IOUringTable doesn't
		// change during an IRQ handler; MaxIORings=4 scan per completion is waste).
		_, ioRing := GetIOUringSlotForBlockIRQ()

		for eng.HasUsed() {
			info := eng.PopUsed()
			if info.Tag == virtio.InvalidIOTag {
				break
			}
			drained++
			tag := uint16(info.Tag)
			meta := &blockAsyncSlots[tag]

			// Read status from sidecar (Device-nGnRnE, no cache mgmt needed)
			status := uint16(0)
			if meta.sidecarStatusVA != 0 {
				status = uint16(*(*uint8)(unsafe.Pointer(meta.sidecarStatusVA)))
			}

			// Invalidate cache on data page so userspace sees device-written data
			if meta.dataKernelVA != 0 && meta.dataLen > 0 {
				asm.InvalidateDCacheRange(meta.dataKernelVA, uintptr(meta.dataLen))
			}

			// Release sidecar slot (bitmap OR — nosplit safe)
			if blockSidecarFreePtr != nil {
				*blockSidecarFreePtr |= uint64(1) << uint(meta.sidecarIdx)
			}

			// Decrement InFlight on DMA clump (if applicable).
			// If the clump is pending release and InFlight hits 0,
			// free the contiguous pages. This handles the case where
			// munmap was called while I/O was in flight.
			if meta.clumpAddr != 0 {
				clump := (*proc.DMAClump)(unsafe.Pointer(meta.clumpAddr))
				remaining := atomic.AddInt32(&clump.InFlight, -1)
				if remaining == 0 && (clump.ShepherdDead || clump.PendingRelease) {
					// Called via indirect pointer to break the nosplit chain.
					// BuddyFreeTyped has large stack frames (buddyRemoveSpecific →
					// buddyRemoveFree → duffzero) that exceed the 792-byte limit
					// when called from the ExceptionVectorTable chain.
					buddyFreeHook(clump.StartPA, clump.BuddyOrder, kmem.PageUserDMA)
				}
			}

			atomic.AddUint32(&dbgBlockAsyncEvents, 1)

			// Write completion: io_uring CQ if active and not full, else legacy path.
			cqWritten := false
			if ioRing != nil {
				cqTail := ioRing.CQTail
				cqHead := atomic.LoadUint32(&ioRing.CQHead)
				if cqTail-cqHead < iouring.CQCapacity {
					atomic.AddUint32(&dbgBlockCQEWritten, 1)
					cqIdx := cqTail & iouring.CQMask
					ioRing.CQEntries[cqIdx] = iouring.CQEntry{
						UserData: meta.userData,
						Res:      int32(info.UsedLen),
					}
					asm.Dsb()
					atomic.StoreUint32(&ioRing.CQTail, cqTail+1)
					cqWritten = true
				}
			}
			if !cqWritten {
				// No active CQ, or CQ full: fall back to the internal soft-IRQ ring.
				atomic.AddUint32(&dbgBlockCQEMissed, 1)
				ev := hid.HIDEvent{
					Type:  tag,
					Code:  status,
					Value: info.UsedLen,
				}
				ringPush(&topHalfBlockRing, ev)
			}

			// Clear metadata slot
			*meta = blockAsyncSlot{}
		}

		// Track drain stats via atomics (nosplit-safe, no printing)
		atomic.AddUint32(&dbgBlockTotalDrained, drained)
		if drained == 0 {
			atomic.AddUint32(&dbgBlockEmptyIRQ, 1)
			// Snapshot raw Used.Idx on first empty-drain (one-shot)
			if atomic.CompareAndSwapUint32(&dbgBlockEmptySnapped, 0, 1) {
				usedVA := uintptr(unsafe.Pointer(eng.VQ.Used))
				asm.InvalidateDCacheRange(usedVA, 4)
				atomic.StoreUint32(&dbgBlockEmptyRawUsedIdx, uint32(asm.MmioRead16(usedVA+2)))
				atomic.StoreUint32(&dbgBlockEmptyLastUsedIdx, uint32(eng.VQ.LastUsedIdx))
				atomic.StoreUint64(&dbgBlockEmptyUsedPtr, uint64(usedVA))
			}
		}
		// Snapshot VQ state for SVC-side logging
		atomic.StoreUint32(&dbgBlockLastNumFree, uint32(eng.VQ.NumFree))
		atomic.StoreUint32(&dbgBlockLastUsedIdx, uint32(eng.VQ.LastUsedIdx))
		atomic.StoreUint32(&dbgBlockLastAvailIdx, uint32(eng.VQ.Available.Idx))

		atomic.AddUint32(&dbgBlockIRQAsync, 1)
		// Wake: try io_uring direct wake, fall back to legacy slot.
		WakeIOUringFromIRQ()
		WakeSlotForIRQ(hid.BlockVirtualIRQ)
	} else {
		// Sync mode: signal IOComplete for the early-boot WFI spin loop.
		// See file header for why this path is retained.
		atomic.AddUint32(&dbgBlockIRQSync, 1)
		if blockIOComplete != nil {
			atomic.StoreUint32(blockIOComplete, 1)
		}
	}
}

// ArmBlockCompletionEvent (MAZ-136) sets the block VirtQ used_event so the device
// raises a single completion interrupt when `minComplete` more requests finish,
// instead of one IRQ per request. Called from SyscallIOUringEnter's submit phase
// just before the doorbell, so the threshold is in place before the device runs.
// Relies on VIRTIO_F_RING_EVENT_IDX being negotiated (virtioBlockInit); without it
// the device ignores used_event and this is a harmless no-op write.
//
//go:nosplit
func ArmBlockCompletionEvent(minComplete uint32) {
	if blockEnginePtr == 0 || minComplete == 0 {
		return
	}
	eng := (*virtio.Engine)(unsafe.Pointer(blockEnginePtr))
	// Interrupt when used.idx reaches LastUsedIdx+minComplete-1 — the index of the
	// batch's final completion. fs serializes block I/O (one batch in flight), so
	// LastUsedIdx equals the device's current used.idx at arm time.
	target := eng.VQ.LastUsedIdx + uint16(minComplete) - 1
	virtio.VirtqueueSetUsedEvent(&eng.VQ, target)
}
