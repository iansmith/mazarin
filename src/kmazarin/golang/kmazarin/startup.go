package main

import "unsafe"

// StartupParams holds argc/argv/envp/auxv structure passed from Cardinal.
// This 16KB BSS buffer eliminates the need for Cardinal's low-memory stack.
//
// Cardinal builds the startup structure (argc/argv/envp/auxv) in temporary
// low-memory, copies it here, then unmaps the temporary memory before jumping
// to kmazarin. This ensures kmazarin never accesses Cardinal's memory space.
//
// Layout (matching Linux convention and Cardinal's setupKmazarinStartupEnv):
//   [0]     = argc (int64, value: 1)
//   [8]     = argv[0] (pointer to program name string at offset 256)
//   [16]    = NULL (end of argv)
//   [24]    = NULL (end of envp, empty environment)
//   [32]    = auxv[0].tag (AT_PAGESZ = 6)
//   [40]    = auxv[0].value (4096)
//   [48]    = auxv[1].tag (AT_RANDOM = 25)
//   [56]    = auxv[1].value (pointer to random bytes at offset 272)
//   ... (more auxv entries)
//   [256]   = program name string ("kmazarin\0")
//   [272]   = random bytes (16 bytes for stack canary)
//
// Size: 16KB provides ample space (512 bytes used + plenty of headroom).
// The Go runtime's rt0_arm64_linux reads this structure directly from SP
// at startup, so kmazarin automatically receives these parameters.
var StartupParams [16384]byte // 16KB BSS allocation

// GetStartupParamsAddr returns the high-memory virtual address of StartupParams.
// This will be 0xFFFFFFFF41xxxxxx (somewhere in kmazarin's BSS section).
//
// Cardinal uses this address (passed via auxv AT_STARTUP_PARAMS) to know where
// to copy the startup structure before jumping to kmazarin.
//
//go:nosplit
func GetStartupParamsAddr() uintptr {
	return uintptr(unsafe.Pointer(&StartupParams[0]))
}
