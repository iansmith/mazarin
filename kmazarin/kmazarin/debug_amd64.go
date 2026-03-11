//go:build amd64

package main

import "unsafe"

// LAPIC register offsets
const (
	lapicHighBase     = 0xFFFFFFFF00000000 + 0xFEE00000
	lapicLVTTimerOff  = 0x320
	lapicTimerInitOff = 0x380
	lapicTimerCurrOff = 0x390
	lapicTimerDivOff  = 0x3E0
)

// printTimerDebug prints comprehensive x86_64 timer and LAPIC debug info.
func printTimerDebug() {
	debugPrintStr("\r\n=== TIMER DEBUG (x86_64) ===\r\n")

	// TSC value
	tsc := ReadTSC()
	debugPrintLabeledHex64("TSC: ", tsc)

	// RFLAGS — check IF (bit 9)
	rflags := ReadRFLAGS()
	debugPrintStr("RFLAGS: 0x")
	debugPrintHex64(rflags)
	debugPrintStr(" (IF=")
	if (rflags>>9)&1 != 0 {
		ForceSerialCharacter('1')
	} else {
		ForceSerialCharacter('0')
	}
	debugPrintStr(")\r\n")

	// LAPIC LVT Timer register
	lvtTimer := *(*uint32)(unsafe.Pointer(uintptr(lapicHighBase + lapicLVTTimerOff)))
	debugPrintStr("LAPIC_LVT_TIMER: 0x")
	debugPrintHex32(lvtTimer)
	debugPrintStr(" (vector=0x")
	debugPrintHex32(lvtTimer & 0xFF)
	debugPrintStr(" masked=")
	if (lvtTimer>>16)&1 != 0 {
		ForceSerialCharacter('1')
	} else {
		ForceSerialCharacter('0')
	}
	debugPrintStr(" mode=")
	mode := (lvtTimer >> 17) & 0x3
	switch mode {
	case 0:
		debugPrintStr("one-shot")
	case 1:
		debugPrintStr("periodic")
	case 2:
		debugPrintStr("tsc-deadline")
	default:
		debugPrintStr("reserved")
	}
	debugPrintStr(")\r\n")

	// LAPIC timer initial count
	initCount := *(*uint32)(unsafe.Pointer(uintptr(lapicHighBase + lapicTimerInitOff)))
	debugPrintLabeledHex32("LAPIC_INIT_COUNT: ", initCount)

	// LAPIC timer current count
	currCount := *(*uint32)(unsafe.Pointer(uintptr(lapicHighBase + lapicTimerCurrOff)))
	debugPrintLabeledHex32("LAPIC_CURR_COUNT: ", currCount)

	// LAPIC timer divide config
	divConfig := *(*uint32)(unsafe.Pointer(uintptr(lapicHighBase + lapicTimerDivOff)))
	debugPrintLabeledHex32("LAPIC_DIV_CONFIG: ", divConfig)

	debugPrintStr("============================\r\n")
}
