// Package shepherd provides APIs specific to shepherd programs.
// Shepherds are userspace processes that host and manage .maz programs.
package shepherd

import (
	"unsafe"
)

// AuxvType represents auxiliary vector entry types.
// Standard Linux types are in the range 0-50.
// Mazzy-specific types start at 512 to avoid conflicts.
type AuxvType uint64

const (
	// Standard Linux auxv types used by Go runtime
	AT_NULL   AuxvType = 0  // End of auxv
	AT_PAGESZ AuxvType = 6  // Page size
	AT_RANDOM AuxvType = 25 // Address of 16 random bytes

	// Mazzy-specific auxv types (must match kernel definitions)
	// Start at 512 to leave room for future Linux additions
	AT_MAZZY_FB_ADDR   AuxvType = 512 // Framebuffer virtual address in shepherd space
	AT_MAZZY_FB_WIDTH  AuxvType = 513 // Framebuffer width in pixels
	AT_MAZZY_FB_HEIGHT AuxvType = 514 // Framebuffer height in pixels
	AT_MAZZY_FB_PITCH  AuxvType = 515 // Framebuffer pitch (bytes per row)
)

// auxvCache caches auxv values after first read
var auxvCache map[AuxvType]uint64
var auxvCacheInitialized bool

// initAuxvCache reads and caches all auxv entries from the stack.
// This must be called early in program startup before the stack is modified.
// The auxv is located after envp on the initial stack.
func initAuxvCache(auxvPtr unsafe.Pointer) {
	if auxvCacheInitialized {
		return
	}
	auxvCache = make(map[AuxvType]uint64)

	// auxv is an array of {type, value} pairs, terminated by AT_NULL
	type auxvEntry struct {
		Type  uint64
		Value uint64
	}

	ptr := uintptr(auxvPtr)
	for {
		entry := (*auxvEntry)(unsafe.Pointer(ptr))
		if AuxvType(entry.Type) == AT_NULL {
			break
		}
		auxvCache[AuxvType(entry.Type)] = entry.Value
		ptr += unsafe.Sizeof(auxvEntry{})
	}

	auxvCacheInitialized = true
}

// InitFromStack initializes the auxv cache from the program's initial stack.
// Call this early in shepherd startup with a pointer to the auxv array.
// The auxv pointer can be found by walking past argc, argv, and envp on the stack.
func InitFromStack(sp uintptr) {
	// Stack layout: argc, argv[0..argc], NULL, envp[0..n], NULL, auxv...
	argc := *(*uint64)(unsafe.Pointer(sp))

	// Skip argc
	ptr := sp + 8

	// Skip argv (argc+1 entries including NULL terminator)
	ptr += uintptr(argc+1) * 8

	// Skip envp (walk until NULL)
	for {
		env := *(*uintptr)(unsafe.Pointer(ptr))
		ptr += 8
		if env == 0 {
			break
		}
	}

	// ptr now points to auxv
	initAuxvCache(unsafe.Pointer(ptr))
}

// GetAuxValue returns the value of the specified auxv entry.
// Returns 0 if the entry is not found (which may be a valid value for some types).
// Use HasAuxValue to check if an entry exists.
func GetAuxValue(t AuxvType) uint64 {
	if !auxvCacheInitialized {
		return 0
	}
	return auxvCache[t]
}

// HasAuxValue returns true if the specified auxv entry exists.
func HasAuxValue(t AuxvType) bool {
	if !auxvCacheInitialized {
		return false
	}
	_, ok := auxvCache[t]
	return ok
}

// Note: For framebuffer info, use sys.GetFramebuffer() instead of auxv.
// The kernel provides a dedicated syscall which is more reliable than parsing auxv.
