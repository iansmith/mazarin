//go:build !test_stubs

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

// RegisterSoftIRQSlotKsyscall registers an IRQ on a soft IRQ slot for a priest.
//
//go:linkname RegisterSoftIRQSlotKsyscall main.RegisterSoftIRQSlotKsyscall
func RegisterSoftIRQSlotKsyscall(irqNum uint32, slotNum int32, priestID int16) int64

// DrainSoftIRQSlotEvents drains events from a soft IRQ slot.
//
//go:linkname DrainSoftIRQSlotEvents main.DrainSoftIRQSlotEvents
func DrainSoftIRQSlotEvents(slotNum int32, buf []hid.HIDEvent, max int) int

// QueryInputDevicesKernel fills device info from discovered input devices.
//
//go:linkname QueryInputDevicesKernel main.QueryInputDevicesKernel
func QueryInputDevicesKernel(infos []hid.InputDeviceInfo, max int) int
