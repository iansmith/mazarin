package bitfield_test

import (
	"fmt"
	"mazarin/bitfield"
)

func ExamplePageFlags() {
	// Create page flags
	flags := bitfield.PageFlags{
		Allocated:  true,
		KernelPage: false,
		Reserved:   0,
	}

	// Pack into uint32
	packed, err := bitfield.PackPageFlags(flags)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Packed flags: 0x%08x\n", packed)

	// Unpack back
	unpacked := bitfield.UnpackPageFlags(packed)
	fmt.Printf("Unpacked - Allocated: %v, KernelPage: %v\n", 
		unpacked.Allocated, unpacked.KernelPage)

	// Output:
	// Packed flags: 0x00000001
	// Unpacked - Allocated: true, KernelPage: false
}















