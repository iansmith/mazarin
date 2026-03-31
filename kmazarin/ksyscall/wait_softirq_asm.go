package ksyscall

import (
	"mazzy/shared/hid"
	_ "unsafe" // for go:linkname
)

// Forward declarations for soft IRQ functions provided via go:linkname.

// ThreadBlockSoftIRQ blocks current thread waiting for soft IRQ.
// Links to wrapper in main package that provides SchedulerFunc.
//
//go:linkname ThreadBlockSoftIRQ main.threadBlockSoftIRQWrapper
func ThreadBlockSoftIRQ(bundlePtr uint64) uintptr

// RegisterSoftIRQDispatcher registers current thread as the soft IRQ dispatcher.
//
//go:linkname RegisterSoftIRQDispatcher main.RegisterSoftIRQDispatcher
func RegisterSoftIRQDispatcher() int64

// GetPendingSoftIRQ drains one item from the overflow queue.
//
//go:linkname GetPendingSoftIRQ main.GetPendingSoftIRQ
func GetPendingSoftIRQ(bundlePtr uint64) bool

// RegisterSoftIRQSlotKsyscall registers an IRQ on a soft IRQ slot for a shepherd.
//
//go:linkname RegisterSoftIRQSlotKsyscall main.RegisterSoftIRQSlotKsyscall
func RegisterSoftIRQSlotKsyscall(irqNum uint32, slotNum int32, shepherdID int16) int64

// DrainSoftIRQSlotEvents drains events from a soft IRQ slot.
//
//go:linkname DrainSoftIRQSlotEvents main.DrainSoftIRQSlotEvents
func DrainSoftIRQSlotEvents(slotNum int32, buf []hid.HIDEvent, max int) int

// QueryInputDevicesKernel fills device info from discovered input devices.
//
//go:linkname QueryInputDevicesKernel main.QueryInputDevicesKernel
func QueryInputDevicesKernel(infos []hid.InputDeviceInfo, max int) int

// BlockOnSlot blocks current thread on a soft IRQ slot.
// Returns context pointer of next thread, or 0.
//
//go:linkname BlockOnSlot main.BlockOnSlot
func BlockOnSlot(slotNum int32) uintptr

// GetSlotInterruptKind returns the InterruptType for a slot.
//
//go:linkname GetSlotInterruptKind main.GetSlotInterruptKind
func GetSlotInterruptKind(slotNum int32) hid.InterruptType

// enableIRQsAndWait atomically enables interrupts and halts until next interrupt.
// On AMD64: STI+HLT+CLI (STI shadow makes this atomic).
// On ARM64: DAIFClr+WFI. On RISC-V: CSRSI+WFI+CSRCI.
//
//go:linkname enableIRQsAndWait main.EnableIRQsAndWait
func enableIRQsAndWait()

// RegisterBlockCompletionRing pins a userspace page and sets it up as the
// shared completion ring for block I/O.
//
//go:linkname RegisterBlockCompletionRing main.RegisterBlockCompletionRing
func RegisterBlockCompletionRing(ringVA uintptr, shepherdID int16) int64

