package main

import "unsafe"

// G0Struct holds a copy of the g0 goroutine struct for kmazarin.
// This allows us to unmap Cardinal's low memory while keeping the Go runtime
// functional, since the g register (x28) will point here instead of Cardinal's g0.
//
// The Go g struct is approximately 400-500 bytes, but we allocate 4KB to be
// safe and allow for future Go version changes.
//
// CRITICAL: Cardinal must copy the g struct here AND update x28 before jumping
// to kmazarin. The compile-time tool discovers this address and passes it to Cardinal.
var G0Struct [4096]byte // 4KB BSS allocation for g struct copy

// GetG0StructAddr returns the high-memory virtual address of G0Struct.
// Cardinal uses this to know where to copy the g struct.
//
//go:nosplit
func GetG0StructAddr() uintptr {
	return uintptr(unsafe.Pointer(&G0Struct[0]))
}

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
//
// CRITICAL: Uninitialized BSS allocation. Cardinal zeros this during ELF loading,
// then populates RuntimeConfig AFTER zeroing. Go runtime does not re-zero BSS.
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
