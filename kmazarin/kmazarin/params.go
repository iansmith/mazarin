// params.go - Parameter buffer parsing for Kmazarin
//
// Kmazarin receives boot-time parameters from Cardinal via a parameter buffer.
// The buffer address and length are passed through custom auxv entries:
//   AT_CARDINAL_PARAMS (0x1000) - physical address of parameter buffer
//   AT_CARDINAL_PARAMS_LEN (0x1001) - length of parameter buffer
//
// This file provides functions to read and parse those parameters.

package main

import (
	"fmt"
	"mazzy/shared/param"
	"unsafe"
)

// maxParamBufferSize is the maximum size of the parameter buffer we can handle
const maxParamBufferSize = 128 * 1024

// paramBuffer holds the parsed parameters from Cardinal
var paramBuffer map[string]string

// initParams reads the parameter buffer from auxv and parses it.
// This should be called early in kmazarin initialization, before the
// Go runtime tries to use any of the parameter values.
//
// The auxv is set up by Cardinal at the top of the stack before jumping
// to kmazarin. We need to walk the auxv to find our custom entries.
func initParams() {
	// For now, this is a placeholder. In a real implementation, we would:
	// 1. Walk the auxv array on the stack (passed by Cardinal)
	// 2. Find AT_CARDINAL_PARAMS and AT_CARDINAL_PARAMS_LEN
	// 3. Map the physical address to a virtual address (if needed)
	// 4. Call param.ParseParamBuffer to parse the buffer
	// 5. Store the result in the global paramBuffer variable

	// Placeholder: create an empty map
	paramBuffer = make(map[string]string)
}

// GetParam retrieves a parameter value by key.
// Returns the value as a string, or empty string if not found.
func GetParam(key string) string {
	if paramBuffer == nil {
		initParams()
	}
	val, _ := param.GetParamString(paramBuffer, key)
	return val
}

// GetParamUint64 retrieves a parameter value as uint64.
// Returns the value and true if found, or (0, false) if not found.
func GetParamUint64(key string) (uint64, bool) {
	if paramBuffer == nil {
		initParams()
	}
	return param.GetParamUint64(paramBuffer, key)
}

// PrintParams prints all parameters to the UART for debugging.
// This is useful during boot to verify Cardinal passed the correct values.
func PrintParams() {
	if paramBuffer == nil {
		initParams()
	}

	fmt.Println("\r\n=== Cardinal Parameters ===")
	for key, value := range paramBuffer {
		fmt.Printf("%s = %s\r\n", key, value)
	}
	fmt.Println("=== End Parameters ===")
}

// readAuxv walks the auxiliary vector on the stack to find our custom entries.
// Returns (buffer_addr, buffer_len, found).
//
// The auxv is an array of uint64 pairs at the top of stack:
//   [argc][argv...][0][envp...][0][auxv...][AT_NULL=0]
//
// We need to parse past argc, argv, and envp to reach the auxv.
func readAuxv() (uintptr, int, bool) {
	// This is a placeholder for the real implementation.
	// In practice, we'd need to:
	// 1. Get the initial stack pointer from Cardinal (passed in a register)
	// 2. Read argc from stack
	// 3. Skip argv array (argc+1 entries, last is NULL)
	// 4. Skip envp array (scan until NULL)
	// 5. Parse auxv entries looking for AT_CARDINAL_PARAMS

	// For now, return not found
	return 0, 0, false
}

// parseAuxvBuffer is a helper that actually reads and parses the parameter buffer
// once we have its physical address and length.
func parseAuxvBuffer(physAddr uintptr, length int) {
	// Validate length before proceeding
	if length <= 0 || length > maxParamBufferSize {
		// Invalid length - cannot parse
		paramBuffer = make(map[string]string)
		return
	}

	// For low-memory (identity-mapped) addresses, we can access directly
	// For high-memory addresses, we'd need to map them first

	// Convert physical address to a Go slice (safe now that we've validated length)
	bufPtr := (*[maxParamBufferSize]byte)(unsafe.Pointer(physAddr))
	buf := bufPtr[:length]

	// Parse the buffer
	paramBuffer = param.ParseParamBuffer(buf, length)
}
