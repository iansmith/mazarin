//go:build amd64

package main

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"unsafe"
)

// dumpLAPICTimerState reads LAPIC timer registers and RFLAGS for debugging.
func dumpLAPICTimerState() {
	lapicBase := uintptr(0xFEE00000 + 0xFFFFFFFF00000000)

	lvtTimer := *(*uint32)(unsafe.Pointer(lapicBase + 0x320))
	sivr := *(*uint32)(unsafe.Pointer(lapicBase + 0xF0))
	initialCount := *(*uint32)(unsafe.Pointer(lapicBase + 0x380))
	currentCount := *(*uint32)(unsafe.Pointer(lapicBase + 0x390))
	divideConfig := *(*uint32)(unsafe.Pointer(lapicBase + 0x3E0))

	rflags := ReadRFLAGS()

	klog.Criticalf("[LAPIC] ", "LVT=0x%x SIVR=0x%x INIT=%d CUR=%d DIV=0x%x RFLAGS=0x%x IF=%d RearmTicks=%d SysFreq=%d\n",
		lvtTimer, sivr,
		initialCount, currentCount, divideConfig,
		rflags, (rflags>>9)&1,
		kirq.TimerRearmTicks, kirq.SystemTimerFrequency)
}
