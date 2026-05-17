// interrupt.go — VirtIO-net IRQ top-half + deferred drain (MAZ-26).
//
// MAZ-26 is the descoped Part 2 of MAZ-21 ("interrupt wiring"). Part 1 shared
// the MSI-X helper into kmazarin/device/virtio/irq (commit fa3ae73); this
// file wires net's interrupt path on top of that.
//
// Architecture (deferred drain — neither block's inline-in-IRQ nor UART's
// goroutine-per-channel):
//
// Block's top-half inlines its ~120-line async-completion path in the IRQ
// context. Net cannot: DrainRx → Engine.Submit → bounds-check panic path
// exceeds the 792-byte nosplit budget. So the nosplit top-half does the
// minimum (ack ISR, DmaRmb, bump counter, raise RxPending) and the drain
// happens in safe Go context. The drain runs INLINE in KernelIdleLoop, not
// via a dedicated goroutine: a goroutine-per-channel hop was tried first but
// the goroutine never got scheduled (KernelIdleLoop never yields by design,
// and async preemption alone didn't pick it up). Running the drain inline in
// the idle loop is in safe Go context anyway, with a full goroutine stack,
// and removes one layer of plumbing.
//
// TX completions need no work here: the MSI-X vector covers the whole device
// so any RX or TX completion fires the same IRQ; the IRQ itself wakes
// SendTx's WFI (see tx.go), and SendTx pops its own TxEng completion. The
// top-half intentionally does not touch TxEng — RxEng/TxEng are disjoint and
// DrainRx/SendTx share no mutable state.
//
// Arch-neutral: MSI-X programming (GICv2m vs LAPIC) and IRQ enable (GIC vs
// no-op) live in irq/msix_{arm64,amd64}.go and irq_setup_{arm64,amd64}.go.
package net

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
)

// RxPending is the IRQ→idle-loop flag: set by the nosplit top-half when an
// RX completion interrupt arrives, cleared by KernelIdleLoop in
// kmazarin/threads.go when it observes the flag and calls
// DrainRxFromBottomHalf inline. Matches uartRxPending /
// kmem.PageTrackingPending pattern. Exported because the bridge lives in
// package main.
var RxPending uint32

// Debug counters — bumped via atomics from the nosplit top-half and the
// idle-loop drain. Read from safe Go context via the accessors below. Cheap
// and useful for future diagnostics; kept past the MAZ-26 bring-up.
var (
	dbgNetIRQCount  uint32 // total net IRQs received
	dbgNetRxDrained uint32 // total RX frames drained (cumulative)
)

// NetIRQTopHalf is called from NonTimerIRQTopHalf (bottom_half.go) when the
// firing IRQ matches netIRQNum. Runs on the exception stack with g set to
// kmazarin g0; must be //go:nosplit and allocation-free. Body is intentionally
// minimal so the nosplit chain fits the 792-byte budget — RX drain happens
// in safe Go context via KernelIdleLoop observing RxPending.
//
//go:nosplit
//go:noinline
func NetIRQTopHalf() {
	atomic.AddUint32(&dbgNetIRQCount, 1)
	if virtioNetDevice.ISRBase != 0 {
		_ = asm.MmioRead8(virtioNetDevice.ISRBase)
	}
	asm.DmaRmb()
	atomic.StoreUint32(&RxPending, 1)
}

// DrainRxFromBottomHalf is the safe-Go-context drain. Called from
// KernelIdleLoop (kmazarin/threads.go) when net.RxPending is observed.
// Updates dbgNetRxDrained.
//
// Why not in the IRQ top-half: DrainRx → Engine.Submit → bounds-check panic
// path exceeds the 792-byte nosplit budget. KernelIdleLoop runs in safe Go
// context with a full goroutine stack, so the drain fits comfortably.
func DrainRxFromBottomHalf() {
	n := virtioNetDevice.DrainRx()
	if n > 0 {
		atomic.AddUint32(&dbgNetRxDrained, uint32(n))
	}
}

// GetIRQNum returns the net device's assigned IRQ number (0 = MSI-X
// configuration did not succeed; net IRQs disabled). Used by main.go to gate
// the SetNetIRQ + enableMSIXDeviceIRQ wire-up.
func GetIRQNum() uint32 {
	return virtioNetDevice.IRQNum
}

// GetISRBase returns the ISR register VA for interrupt acknowledgement
// (MAZ-28 step 2). Used by the kernel IRQ top-half to ack net IRQs.
func GetISRBase() uintptr {
	return virtioNetDevice.ISRBase
}

// GetRxEnginePtr returns a uintptr to the RX Engine (MAZ-28 step 2). The
// IRQ top-half pops completions from this engine — stored as uintptr so
// bottom_half.go doesn't need to import this package (nosplit-safe).
func GetRxEnginePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice.RxEng))
}

// GetTxEnginePtr returns a uintptr to the TX Engine (MAZ-28 step 3).
// The IRQ top-half drains TX completions from this engine alongside
// the RX drain.
func GetTxEnginePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice.TxEng))
}

// GetDevicePtr returns a uintptr to the global VirtIONetDevice (MAZ-28
// step 2). Used by the IRQ top-half to write RxIRQTimestamps[tag] in
// nosplit context.
func GetDevicePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioNetDevice))
}

// GetDevice returns a typed pointer to the global VirtIONetDevice
// (MAZ-28 step 2). Used by the SQE dispatcher for IOUringOpNetRearmDesc.
func GetDevice() *VirtIONetDevice {
	return &virtioNetDevice
}

// ReadRxIRQTimestamp returns the kernel-recorded IRQ timestamp (ns) for
// the given RX descriptor tag (MAZ-28 step 2). Called via syscall from
// net.elf after dequeuing a CQE to compute IRQ→shepherd latency.
//
// Returns 0 if tag is out of range.
func ReadRxIRQTimestamp(tag uint16) uint64 {
	if int(tag) >= len(virtioNetDevice.RxIRQTimestamps) {
		return 0
	}
	return atomic.LoadUint64(&virtioNetDevice.RxIRQTimestamps[tag])
}

// GetNetIRQCount returns the cumulative count of net IRQs handled by the
// top-half. Safe to call from any context.
func GetNetIRQCount() uint32 {
	return atomic.LoadUint32(&dbgNetIRQCount)
}

// GetNetRxDrained returns the cumulative count of RX frames drained across
// all idle-loop wakeups. Safe to call from any context.
func GetNetRxDrained() uint32 {
	return atomic.LoadUint32(&dbgNetRxDrained)
}
