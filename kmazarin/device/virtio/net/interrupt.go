// interrupt.go — VirtIO-net accessors used by the kernel-main package's
// IRQ top-half (MAZ-28 step 2/3).
//
// MAZ-26's deferred-drain mechanism (RxPending flag + DrainRxFromBottomHalf)
// is gone — the IRQ top-half drains both RxEng and TxEng inline and pushes
// io_uring CQEs, see kmazarin/kmazarin/net_top_half.go. The handful of
// thin accessors below are all that the IRQ branch needs to find the
// device pointers without importing this package transitively.
package net

import (
	"sync/atomic"
	"unsafe"
)

// GetIRQNum returns the net device's assigned IRQ number (0 = MSI-X
// configuration did not succeed; net IRQs disabled). Used by main.go to
// gate the SetNetIRQ + enableMSIXDeviceIRQ wire-up.
func GetIRQNum() uint32 {
	return virtioNetDevice.IRQNum
}

// GetISRBase returns the ISR register VA for interrupt acknowledgement.
func GetISRBase() uintptr {
	return virtioNetDevice.ISRBase
}

// GetRxEnginePtr returns a uintptr to the RX Engine (stored as uintptr
// so the kernel's nosplit IRQ top-half doesn't need to import this
// package — see kmazarin/kmazarin/net_top_half.go).
func GetRxEnginePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice.RxEng))
}

// GetTxEnginePtr returns a uintptr to the TX Engine.
func GetTxEnginePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice.TxEng))
}

// GetDevicePtr returns a uintptr to the global VirtIONetDevice. Used by
// the IRQ top-half to write RxIRQTimestamps[tag] / read TxTagByDescIdx
// in nosplit context.
func GetDevicePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice))
}

// GetDevice returns a typed pointer to the global VirtIONetDevice. Used
// by the SQE dispatcher for IOUringOpNetRearmDesc / IOUringOpNetSubmitTx.
func GetDevice() *VirtIONetDevice {
	return &virtioNetDevice
}

// ReadRxIRQTimestamp returns the kernel-recorded IRQ timestamp (platform
// timer ticks) for the given RX descriptor tag. Called via syscall from
// net.elf after dequeuing a CQE to compute IRQ→shepherd latency.
//
// Returns 0 if tag is out of range.
func ReadRxIRQTimestamp(tag uint16) uint64 {
	if int(tag) >= len(virtioNetDevice.RxIRQTimestamps) {
		return 0
	}
	return atomic.LoadUint64(&virtioNetDevice.RxIRQTimestamps[tag])
}
