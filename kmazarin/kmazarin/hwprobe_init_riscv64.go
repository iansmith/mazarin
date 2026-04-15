//go:build riscv64

package main

import (
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/ksyscall"
)

// discoveredISAString holds the ISA string from the DTB, discovered at boot.
var discoveredISAString string

// initHWProbeFromDTB walks the DTB to find the "riscv,isa" property from the
// first CPU node and initializes the riscv_hwprobe syscall handler with the
// detected ISA extensions.
func initHWProbeFromDTB() {
	var dtbAddr uintptr
	if safeDTBVirtAddr != 0 {
		dtbAddr = safeDTBVirtAddr
	} else {
		dtbPhysAddr := GetDtbPhysAddr()
		if dtbPhysAddr != 0 {
			dtbAddr = dtbVirtAddr(dtbPhysAddr)
		}
	}
	if dtbAddr == 0 {
		return
	}

	discoveredISAString = ""
	dtb.Walk(dtbAddr, dtbISACallback)

	if discoveredISAString != "" {
		klog.Logf("[HWProbe] ISA from DTB: %s\n", discoveredISAString)
		ksyscall.InitHWProbe(discoveredISAString)
	} else {
		klog.Logf("[HWProbe] no ISA string found in DTB\n")
	}
}

// dtbISACallback extracts the "riscv,isa" property from CPU nodes.
// Only takes the first CPU's ISA string (all cores are identical on QEMU virt).
func dtbISACallback(node *dtb.DTBNode) {
	if discoveredISAString != "" {
		return // already found
	}
	var nameBuf [64]byte
	nameLen := node.GetNameBuf(nameBuf[:])
	name := string(nameBuf[:nameLen])
	// CPU nodes are named "cpu@0", "cpu@1", etc.
	if len(name) >= 4 && name[:4] == "cpu@" {
		if isa, ok := node.GetPropString("riscv,isa"); ok {
			discoveredISAString = isa
		}
	}
}
