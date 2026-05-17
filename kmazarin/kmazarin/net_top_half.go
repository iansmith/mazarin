// net_top_half.go — Net IRQ top-half (MAZ-28 steps 2+3).
//
// Factored out of bottom_half.go so the dispatcher's nosplit frame
// stays small — the dispatcher's frame is the max of all branches, so
// keeping net's locals (rxEng/txEng/dev/ioRing/descTable/descStride/
// info/tag/...) in their own function lets the dispatcher's frame stay
// at the previous size while the net branch keeps its locals here.
//
// RX uses slot-pinned PopUsedNoFree (net.elf re-arms via
// IOUringOpNetRearmDesc). TX uses PopUsed with a TxTagByDescIdx lookup
// for the CQE encoding. Wake at the end via WakeIOUringFromIRQ +
// WakeSlotForIRQ(NetVirtualIRQ).

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/device/virtio/net"
	"mazzy/kmazarin/kirq"
	"mazzy/shared/constants"
	"mazzy/shared/hid"
	"mazzy/shared/iouring"
	"sync/atomic"
	"unsafe"
)

//go:nosplit
func netTopHalf() {
	atomic.AddUint32(&dbgNetIRQCount, 1)
	if netISRBase != 0 {
		_ = asm.MmioRead8(netISRBase) // Ack interrupt
	}
	asm.DmaRmb()

	now := kirq.ReadCounterValue()
	rxEng := (*virtio.Engine)(unsafe.Pointer(netRxEnginePtr))
	txEng := (*virtio.Engine)(unsafe.Pointer(netTxEnginePtr))
	dev := (*net.VirtIONetDevice)(unsafe.Pointer(netDevicePtr))
	_, ioRing := GetIOUringSlotForNetIRQ()
	descTable := uintptr(rxEng.VQ.DescTable)
	descStride := unsafe.Sizeof(virtio.VirtQDesc{})

	// RX drain — slot-pinned (PopUsedNoFree).
	for rxEng.HasUsed() {
		info := rxEng.PopUsedNoFree()
		if info.Tag == virtio.InvalidIOTag {
			break
		}
		tag := uint16(info.Tag)
		atomic.AddUint32(&dbgNetRxDrained, 1)

		if int(tag) < len(dev.RxIRQTimestamps) {
			atomic.StoreUint64(&dev.RxIRQTimestamps[tag], now)
		}

		desc := (*virtio.VirtQDesc)(unsafe.Pointer(descTable + uintptr(tag)*descStride))
		framePA := uintptr(desc.Addr)
		if framePA != 0 && info.UsedLen > 0 {
			frameKernelVA := framePA + constants.KernelVAOffset
			asm.InvalidateDCacheRange(frameKernelVA, uintptr(info.UsedLen))
		}

		if ioRing != nil {
			atomic.AddUint32(&dbgNetCQEWritten, 1)
			cqTail := ioRing.CQTail
			cqIdx := cqTail & iouring.CQMask
			ioRing.CQEntries[cqIdx] = iouring.CQEntry{
				UserData: iouring.NetEncodeRxUserData(tag),
				Res:      int32(info.UsedLen),
			}
			asm.Dsb()
			atomic.StoreUint32(&ioRing.CQTail, cqTail+1)
		} else {
			atomic.AddUint32(&dbgNetCQEMissed, 1)
		}
	}

	// TX drain — descriptor chain returns to free list via PopUsed.
	for txEng.HasUsed() {
		info := txEng.PopUsed()
		if info.Tag == virtio.InvalidIOTag {
			break
		}
		descIdx := uint16(info.Tag)
		atomic.AddUint32(&dbgNetTxDrained, 1)

		var txTag uint16
		if int(descIdx) < len(dev.TxTagByDescIdx) {
			txTag = dev.TxTagByDescIdx[descIdx]
		}

		if ioRing != nil {
			atomic.AddUint32(&dbgNetCQEWritten, 1)
			cqTail := ioRing.CQTail
			cqIdx := cqTail & iouring.CQMask
			ioRing.CQEntries[cqIdx] = iouring.CQEntry{
				UserData: iouring.NetEncodeTxUserData(txTag),
				Res:      0,
			}
			asm.Dsb()
			atomic.StoreUint32(&ioRing.CQTail, cqTail+1)
		} else {
			atomic.AddUint32(&dbgNetCQEMissed, 1)
		}
	}

	WakeIOUringFromIRQ()
	WakeSlotForIRQ(hid.NetVirtualIRQ)
}
