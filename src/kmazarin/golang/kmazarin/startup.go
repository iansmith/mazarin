package main

import "unsafe"

// StartupParams holds runtime configuration and argc/argv/envp/auxv structure.
// This 16KB BSS buffer eliminates the need for Cardinal's low-memory stack.
//
// Cardinal populates this buffer before jumping to kmazarin, providing both
// the runtime configuration (for immediate use) and standard startup parameters
// (for Go runtime initialization).
//
// Layout:
//   [0-255]   = RuntimeConfig struct (populated by Cardinal)
//   [256]     = argc (int64, value: 1)  <- SP points here for Go runtime
//   [264]     = argv[0] (pointer to program name string at offset 512)
//   [272]     = NULL (end of argv)
//   [280]     = NULL (end of envp, empty environment)
//   [288]     = auxv[0].tag (AT_PAGESZ = 6)
//   [296]     = auxv[0].value (4096)
//   [304]     = auxv[1].tag (AT_RANDOM = 25)
//   [312]     = auxv[1].value (pointer to random bytes at offset 528)
//   ... (more auxv entries)
//   [512]     = program name string ("kmazarin\0")
//   [528]     = random bytes (16 bytes for stack canary)
//
// Size: 16KB provides ample space.
// RuntimeConfig is at offset 0 for immediate access.
// argc/argv/envp/auxv starts at offset 256 (SP points there).
var StartupParams [16384]byte // 16KB BSS allocation

// RuntimeConfigOffset is the offset of RuntimeConfig within StartupParams
const RuntimeConfigOffset = 0

// ArgcOffset is the offset where argc/argv/envp/auxv begins (for Go runtime)
const ArgcOffset = 256

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
