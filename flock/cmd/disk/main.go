// disk is a userspace priest that owns the block device and loads a
// filesystem .maz module to serve IPC requests. It discovers the block
// device, registers for ownership via SoftIRQ, then loads fs.maz which
// mounts FAT32 and enters the IPC serve loop.
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"os"
	"unsafe"
)

func main() {
	fmt.Println("[disk] starting disk priest")

	// 1. Query devices to find the block device virtual IRQ
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[disk] QueryInputDevices failed: %v\n", err)
		os.Exit(1)
	}

	blockSlot := -1
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeBlock {
			// Register for block device ownership
			err := sys.RegisterSoftIRQ(dev.IRQNum, 0) // slot 0
			if err != nil {
				fmt.Printf("[disk] RegisterSoftIRQ for block device failed: %v\n", err)
				os.Exit(1)
			}
			blockSlot = 0
			fmt.Printf("[disk] registered block device IRQ %d on slot %d\n", dev.IRQNum, blockSlot)
			break
		}
	}

	if blockSlot < 0 {
		fmt.Println("[disk] ERROR: no block device found")
		os.Exit(1)
	}

	// 2. Load filesystem .maz module
	fmt.Println("[disk] loading filesystem maz...")
	mazResult, mazErr := sys.LoadMaz("/fs.maz")
	if mazErr != nil {
		fmt.Printf("[disk] LoadMaz failed: %v\n", mazErr)
		os.Exit(1)
	}
	fmt.Printf("[disk] fs.maz loaded: entry=0x%X base=0x%X size=0x%X\n",
		mazResult.EntryPoint, mazResult.LoadBase, mazResult.LoadSize)

	// 3. Register .maz moduledata for stack trace support
	sys.RegisterMazModule(mazResult)

	// 4. Call the loaded entry point (MazarinMain — never returns)
	type funcval struct{ fn uintptr }
	fv := &funcval{fn: uintptr(mazResult.EntryPoint)}
	mazMain := *(*func())(unsafe.Pointer(&fv))
	mazMain()
}
