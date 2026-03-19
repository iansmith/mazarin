// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// KMAZARIN OVERLAY: Replaces the no-op archauxv with one that parses custom
// auxv entries for heap bounds and kernel configuration.
// This allows diplomat to pass configuration to the runtime overlay
// BEFORE the first mmap call, enabling dynamic heap sizing.
//
// On x86_64: kmazarinUART uses COM1 (port 0x3F8) via assembly helper in sys_linux_amd64.s
// On ARM64:  kmazarinUART writes directly to MMIO UART (defined in os_linux_arm64.go)

//go:build linux && !arm && !arm64 && !loong64 && !mips && !mipsle && !mips64 && !mips64le && !s390x && !ppc64 && !ppc64le

package runtime

// Custom auxv tags for kmazarin (must match shared/constants/auxv.go)
const (
	_AT_DTB_PHYS            = 0x1000
	_AT_KMAZARIN_SIZE       = 0x1003
	_AT_FRAME_POOL_START    = 0x1004
	_AT_FRAME_POOL_END      = 0x1005
	_AT_TTBR1_L0_PHYS       = 0x1008 // PML4 phys on x86_64
	_AT_CPU_COUNT            = 0x100C
	_AT_RAM_BASE             = 0x100D
	_AT_RAM_SIZE             = 0x100E
	_AT_UNIFIED_POOL_START   = 0x100F
	_AT_KMAZARIN_HEAP_START  = 0x1010
	_AT_KMAZARIN_HEAP_END    = 0x1011
	_AT_UNIFIED_POOL_END     = 0x1012
	_AT_TTBR0_L0_PHYS       = 0x1013 // CR3 phys on x86_64
	_AT_KERNEL_BUDGET_MB     = 0x1014
)

// Kernel configuration values - set by archauxv before Go runtime init completes.
// Accessed by kmazarin packages via go:linkname.
// Initial values of 0 indicate "not yet initialized from auxv".
var kmazarinHeapStart uintptr
var kmazarinHeapEnd uintptr
var kmazarinTTBR1L0Phys uintptr // PML4 on x86_64
var kmazarinTTBR0L0Phys uintptr // CR3 on x86_64
var kmazarinFramePoolStart uintptr
var kmazarinFramePoolEnd uintptr
var kmazarinUnifiedPoolStart uintptr
var kmazarinUnifiedPoolEnd uintptr
var kmazarinKmazarinSize uintptr
var kmazarinDtbPhysAddr uintptr
var kmazarinCPUCount uintptr
var kmazarinRAMBase uintptr
var kmazarinRAMSize uintptr
var kmazarinKernelBudgetMB uintptr

// suppressSerial is set to 1 by kmazarin when the SoftIRQ console activates.
// When set, write1 skips UART output (serial copy is redundant once stdio
// shepherd receives kernel output via the ring buffer).
var suppressSerial uint32

// kmazarinUART writes a byte to COM1 (0x3F8) for early boot diagnostics.
// Implemented in sys_linux_amd64.s using port I/O (OUTB).
//
//go:nosplit
func kmazarinUART(c byte)

func archauxv(tag, val uintptr) {
	switch tag {
	case _AT_KMAZARIN_HEAP_START:
		kmazarinHeapStart = val
	case _AT_KMAZARIN_HEAP_END:
		kmazarinHeapEnd = val
	case _AT_TTBR1_L0_PHYS:
		kmazarinTTBR1L0Phys = val
	case _AT_TTBR0_L0_PHYS:
		kmazarinTTBR0L0Phys = val
	case _AT_FRAME_POOL_START:
		kmazarinFramePoolStart = val
	case _AT_FRAME_POOL_END:
		kmazarinFramePoolEnd = val
	case _AT_UNIFIED_POOL_START:
		kmazarinUnifiedPoolStart = val
	case _AT_UNIFIED_POOL_END:
		kmazarinUnifiedPoolEnd = val
	case _AT_KMAZARIN_SIZE:
		kmazarinKmazarinSize = val
	case _AT_DTB_PHYS:
		kmazarinDtbPhysAddr = val
	case _AT_CPU_COUNT:
		kmazarinCPUCount = val
	case _AT_RAM_BASE:
		kmazarinRAMBase = val
	case _AT_RAM_SIZE:
		kmazarinRAMSize = val
	case _AT_KERNEL_BUDGET_MB:
		kmazarinKernelBudgetMB = val
	}
}
