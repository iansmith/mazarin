package device

import (
	"mazzy/kmazarin/arch/riscv64/plic"
)

// ArchSpecificDrivers contains RISC-V-specific device drivers.
//
// These drivers use RISC-V-specific hardware:
// - PLIC (Platform-Level Interrupt Controller) for external interrupts
//
// The PLIC is discovered via DTB with compatible strings
// "sifive,plic-1.0.0" or "riscv,plic0".
var ArchSpecificDrivers = []Discoverable{
	&plic.PLICDriver{},
}
