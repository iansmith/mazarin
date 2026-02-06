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

// StartupParams is a 16KB BSS buffer used by Cardinal to pass runtime configuration.
// Cardinal populates this with RuntimeConfig + argc/argv/envp/auxv before jumping.
//
// NOTE: With the diplomat bootloader, all config is passed via auxv entries
// (parsed by archauxv during runtime init). StartupParams is kept only for
// cardinal compatibility. Kmazarin code should use auxv-backed vars, not
// read from this buffer directly.
var StartupParams [16384]byte // 16KB BSS allocation

// GetStartupParamsAddr returns the high-memory virtual address of StartupParams.
// Used by cardinal's linker-value tool to find the buffer address.
//
//go:nosplit
func GetStartupParamsAddr() uintptr {
	return uintptr(unsafe.Pointer(&StartupParams[0]))
}
