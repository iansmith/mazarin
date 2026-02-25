//go:build amd64 && !test_stubs

package main

import (
	"mazzy/kmazarin/serial"
	"unsafe"
)

//go:linkname allglen runtime.allglen
var allglen uintptr

//go:linkname allgptr runtime.allgptr
var allgptr unsafe.Pointer

// Offsets within the runtime.g struct (Go 1.25.5, amd64):
// Verified against runtime.mcall disassembly:
//   g.m         = 0x30
//   g.sched.sp  = 0x38 (g_sched + gobuf_sp)
//   g.sched.pc  = 0x40 (g_sched + gobuf_pc)
//   g.sched.bp  = 0x60 (g_sched + gobuf_bp)
const (
	gOffSchedSP = 0x38
	gOffSchedPC = 0x40
	gOffGoid    = 0x98
	gOffStatus  = 0x90
	gOffStackLo = 0x00
	gOffStackHi = 0x08
)

// Status names for g.atomicstatus
var statusNames = [9]string{
	"idle", "rble", "rung", "sysc", "wait", "?5", "dead", "?7", "copy",
}

// scanAllgsForCorruptSP iterates the runtime's allgs list and reports
// ALL goroutines' sched.sp values and status for debugging.
func scanAllgsForCorruptSP() {
	n := allglen
	if n == 0 || allgptr == nil {
		serial.RawUARTPuts("[diag] allgs empty\r\n")
		return
	}

	serial.RawUARTPuts("[diag] ")
	serial.RawUARTHex64(uint64(n))
	serial.RawUARTPuts(" goroutines:\r\n")

	base := uintptr(allgptr)
	badCount := 0

	for i := uintptr(0); i < n; i++ {
		gp := *(*uintptr)(unsafe.Pointer(base + i*8))
		if gp == 0 {
			continue
		}

		status := *(*uint32)(unsafe.Pointer(gp + gOffStatus))
		rawStatus := status & 0xFFF
		schedSP := *(*uint64)(unsafe.Pointer(gp + gOffSchedSP))
		schedPC := *(*uint64)(unsafe.Pointer(gp + gOffSchedPC))
		goid := *(*uint64)(unsafe.Pointer(gp + gOffGoid))
		stackLo := *(*uint64)(unsafe.Pointer(gp + gOffStackLo))
		stackHi := *(*uint64)(unsafe.Pointer(gp + gOffStackHi))

		// Print: "g<idx> id=<goid> st=<status> sp=<sp> pc=<pc> stk=<lo>-<hi>"
		serial.RawUARTPuts(" g")
		serial.RawUARTDecimal(uint64(i))
		serial.RawUARTPuts(" id=")
		serial.RawUARTDecimal(goid)
		serial.RawUARTPuts(" ")
		if rawStatus < 9 {
			serial.RawUARTPuts(statusNames[rawStatus])
		} else {
			serial.RawUARTPuts("?")
			serial.RawUARTHex8(uint8(rawStatus))
		}
		if status&0x1000 != 0 {
			serial.RawUARTPuts("+S") // _Gscan bit set
		}
		serial.RawUARTPuts(" sp=")
		serial.RawUARTHex64(schedSP)
		serial.RawUARTPuts(" pc=")
		serial.RawUARTHex64(schedPC)

		// Flag suspicious SP values
		if schedSP != 0 && schedSP < 0x100000 && rawStatus != 6 && rawStatus != 0 {
			serial.RawUARTPuts(" *** BAD ***")
			badCount++
		}

		// For non-dead, non-idle goroutines with sp != 0, check if sp is within stack bounds
		if schedSP != 0 && rawStatus != 6 && rawStatus != 0 && stackLo != 0 && stackHi != 0 {
			if schedSP < stackLo || schedSP > stackHi {
				serial.RawUARTPuts(" *** OUT-OF-BOUNDS ***")
				badCount++
			}
		}

		serial.RawUARTPuts("\r\n")
	}

	if badCount > 0 {
		serial.RawUARTPuts("[diag] found ")
		serial.RawUARTDecimal(uint64(badCount))
		serial.RawUARTPuts(" corrupt goroutines\r\n")
	}
}

// launchAllgsDiagnostic scans all goroutines for corrupt sched.sp values.
func launchAllgsDiagnostic() {
	scanAllgsForCorruptSP()
}
