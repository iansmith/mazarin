//go:build qemuvirt && aarch64

// Mazzy userspace runtime overlay: RuntimeTable for trampoline dispatch
//
// This file is overlaid onto the Go runtime package for userspace programs.
// It provides the RuntimeTable that trampolines use to jump to priest's runtime.
//
// The table is populated by the patchruntime tool after compilation.
package runtime

// RuntimeTableSize is the maximum number of runtime functions that can be trampolined.
// This should be larger than the number of runtime functions (~1400).
const RuntimeTableSize = 2048

// RuntimeTable holds addresses of priest's runtime functions.
// Trampolines load from this table and jump to the target.
// Index N contains the address of the Nth runtime function (sorted alphabetically).
//
// This variable is patched by patchruntime after compilation.
var RuntimeTable [RuntimeTableSize]uintptr
