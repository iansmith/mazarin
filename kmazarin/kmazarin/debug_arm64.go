//go:build arm64 && !test_stubs

package main

import "unsafe"

// printTimerDebug prints comprehensive ARM64 timer and GIC debug info.
// This is a debugging aid — not called in normal operation.
// Uses neutral debugPrint* utilities from debug.go and ForceSerialCharacter.
func printTimerDebug() {
	const gicDist = 0x08000000

	debugPrintStr("\r\n=== TIMER DEBUG ===\r\n")

	ctl := ReadCntvCtlEl0()
	tval := ReadCntvTvalEl0()
	cval := ReadCntvctEl0()
	freq := ReadCntfrqEl0()
	daif := ReadDAIF()

	debugPrintStr("CNTV_CTL_EL0: 0x")
	debugPrintHex32(uint32(ctl))
	debugPrintStr(" (enable=")
	ForceSerialCharacter('0' + byte(ctl&1))
	debugPrintStr(" mask=")
	ForceSerialCharacter('0' + byte((ctl>>1)&1))
	debugPrintStr(" status=")
	ForceSerialCharacter('0' + byte((ctl>>2)&1))
	debugPrintStr(")\r\n")

	debugPrintStr("CNTV_TVAL_EL0: ")
	if (tval & 0x80000000) != 0 {
		ForceSerialCharacter('-')
		debugPrintHex32(uint32(-int32(tval)))
	} else {
		debugPrintHex32(uint32(tval))
	}
	debugPrintNewline()

	debugPrintLabeledHex64("CNTVCT_EL0: ", cval)
	debugPrintLabeledHex32("CNTFRQ_EL0: ", uint32(freq))

	debugPrintStr("DAIF: 0x")
	debugPrintHex32(uint32(daif))
	debugPrintStr(" (I=")
	ForceSerialCharacter('0' + byte((daif>>7)&1))
	debugPrintStr(")\r\n")

	// GIC register dump
	isenabler := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x100)))
	debugPrintStr("GICD_ISENABLER0: 0x")
	debugPrintHex32(isenabler)
	debugPrintStr(" (bit27=")
	ForceSerialCharacter('0' + byte((isenabler>>27)&1))
	debugPrintStr(")\r\n")

	ispendr := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x200)))
	debugPrintStr("GICD_ISPENDR0: 0x")
	debugPrintHex32(ispendr)
	debugPrintStr(" (bit27=")
	ForceSerialCharacter('0' + byte((ispendr>>27)&1))
	debugPrintStr(")\r\n")

	isactiver := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x300)))
	debugPrintStr("GICD_ISACTIVER0: 0x")
	debugPrintHex32(isactiver)
	debugPrintStr(" (bit27=")
	ForceSerialCharacter('0' + byte((isactiver>>27)&1))
	debugPrintStr(")\r\n")

	ipriority6 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x400 + 24)))
	debugPrintStr("GICD_IPRIORITYR6: 0x")
	debugPrintHex32(ipriority6)
	debugPrintStr(" (IRQ27 pri=")
	debugPrintHex32((ipriority6 >> 24) & 0xFF)
	debugPrintStr(")\r\n")

	itargets6 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x800 + 24)))
	debugPrintStr("GICD_ITARGETSR6: 0x")
	debugPrintHex32(itargets6)
	debugPrintStr(" (IRQ27 cpu=")
	debugPrintHex32((itargets6 >> 24) & 0xFF)
	debugPrintStr(")\r\n")

	const gicCpu = 0x08010000
	debugPrintLabeledHex32("GICC_PMR: ", *(*uint32)(unsafe.Pointer(uintptr(gicCpu+0x004))))
	debugPrintLabeledHex32("GICD_CTLR: ", *(*uint32)(unsafe.Pointer(uintptr(gicDist+0x000))))
	debugPrintLabeledHex32("GICC_CTLR: ", *(*uint32)(unsafe.Pointer(uintptr(gicCpu+0x000))))

	igroupr0 := *(*uint32)(unsafe.Pointer(uintptr(gicDist + 0x080)))
	debugPrintStr("GICD_IGROUPR0: 0x")
	debugPrintHex32(igroupr0)
	debugPrintStr(" (IRQ27 grp=")
	ForceSerialCharacter('0' + byte((igroupr0>>27)&1))
	debugPrintStr(")\r\n")

	debugPrintStr("==================\r\n")
}
