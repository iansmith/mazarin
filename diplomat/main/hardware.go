// diplomat/main/hardware.go - Architecture-neutral hardware info types

package main

// HardwareInfo holds hardware information discovered from UEFI.
type HardwareInfo struct {
	CPUCount uint64 // Number of processors
	RAMBase  uint64 // Physical base address of RAM
	RAMSize  uint64 // Total RAM size in bytes
}
