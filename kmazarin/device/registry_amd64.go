package device

import (
	"mazzy/kmazarin/arch/amd64/apic"
)

// ArchSpecificDrivers contains x86_64-specific device drivers.
//
// These drivers use x86-specific hardware:
// - Local APIC for per-CPU interrupt delivery and timer
// - I/O APIC for external interrupt routing
//
// Note: x86_64 QEMU uses ACPI (not DTB) for device discovery.
// The APIC driver uses hardcoded QEMU virt addresses and Probe
// always returns true.
var ArchSpecificDrivers = []Discoverable{
	&apic.APICDriver{},
}
