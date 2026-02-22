package device

import (
	"mazzy/kmazarin/arch/amd64/apic"
	"mazzy/kmazarin/rtc"
)

// ArchSpecificDrivers contains x86_64-specific device drivers.
//
// These drivers use x86-specific hardware:
// - Local APIC for per-CPU interrupt delivery and timer
// - I/O APIC for external interrupt routing
// - CMOS RTC for wall-clock time (I/O ports 0x70-0x71)
//
// Note: x86_64 QEMU uses ACPI (not DTB) for device discovery.
// The APIC driver uses hardcoded QEMU virt addresses and Probe
// always returns true.
var ArchSpecificDrivers = []Discoverable{
	&apic.APICDriver{},
	&rtc.CMOSRTCDriver{},
}
