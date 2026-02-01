//go:build arm64

package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/shared/hid"
	"sync/atomic"
)

// ============================================================================
// Soft IRQ Slot System
// ============================================================================
//
// Maps (IRQ number, priest ID) → slot number → device.
// Userspace registers slots, and the kernel drains events from the
// corresponding VirtIO input device when the slot is polled.

const maxSoftIRQSlots = 32

// softIRQSlot represents one registered soft IRQ slot.
type softIRQSlot struct {
	active   uint32 // atomic: 1 = active
	irqNum   uint32
	priestID int16
	devIdx   int // index into input.AllDevices()
}

var softIRQSlots [maxSoftIRQSlots]softIRQSlot

// irqToSlot maps IRQ numbers to slot indices for fast lookup from IRQ context.
var irqToSlot [256]int32 // -1 = no mapping

func init() {
	for i := range irqToSlot {
		irqToSlot[i] = -1
	}
}

// SoftIRQSlotFire is called from the input device IRQ handler (via softIRQFireFunc).
// It marks the slot as having pending events.
//
//go:nosplit
func SoftIRQSlotFire(irqNum uint32) {
	if irqNum >= 256 {
		return
	}
	// Nothing extra needed — PendingEvents on the device is already incremented
	// by HandleIRQ. The slot just maps IRQ→device for DrainSoftIRQSlotEvents.
}

// RegisterSoftIRQSlotKsyscall registers an IRQ on a soft IRQ slot for a priest.
// Called from ksyscall via linkname.
func RegisterSoftIRQSlotKsyscall(irqNum uint32, slotNum int32, priestID int16) int64 {
	if slotNum < 0 || slotNum >= maxSoftIRQSlots {
		return -22 // EINVAL
	}

	// Find which device has this IRQ
	devIdx := -1
	devices := input.AllDevices()
	for i, dev := range devices {
		if dev != nil && dev.IRQNum == irqNum {
			devIdx = i
			break
		}
	}
	if devIdx < 0 {
		console.KPrintf("[SoftIRQSlot] No device found for IRQ %d\n", irqNum)
		return -19 // ENODEV
	}

	slot := &softIRQSlots[slotNum]
	slot.irqNum = irqNum
	slot.priestID = priestID
	slot.devIdx = devIdx
	atomic.StoreUint32(&slot.active, 1)

	if irqNum < 256 {
		irqToSlot[irqNum] = slotNum
	}

	console.KPrintf("[SoftIRQSlot] Registered slot %d: IRQ=%d priest=%d dev=%d\n",
		slotNum, irqNum, priestID, devIdx)
	return 0
}

// DrainSoftIRQSlotEvents drains events from the device associated with a slot.
// Called from ksyscall via linkname.
func DrainSoftIRQSlotEvents(slotNum int32, buf []hid.HIDEvent, max int) int {
	if slotNum < 0 || slotNum >= maxSoftIRQSlots {
		return 0
	}
	slot := &softIRQSlots[slotNum]
	if atomic.LoadUint32(&slot.active) == 0 {
		return 0
	}

	devices := input.AllDevices()
	if slot.devIdx >= len(devices) {
		return 0
	}
	dev := devices[slot.devIdx]
	if dev == nil {
		return 0
	}

	return dev.DrainEvents(buf, max)
}

// QueryInputDevicesKernel fills device info from discovered input devices.
// Called from ksyscall via linkname.
func QueryInputDevicesKernel(infos []hid.InputDeviceInfo, max int) int {
	devices := input.AllDevices()
	n := 0
	for _, dev := range devices {
		if n >= max || n >= len(infos) {
			break
		}
		if dev == nil {
			continue
		}
		infos[n] = hid.InputDeviceInfo{
			IRQNum:     dev.IRQNum,
			DeviceType: dev.DevType,
		}
		n++
	}
	return n
}
