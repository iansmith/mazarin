// print-kmazarin-addr.go - Print kmazarin load address from constants
//
// This tool prints the kmazarin load address computed from the memory layout
// constants defined in cardinal/constants package.
//
// Used by: tools/kmazarin-entry.sh (replaces parsing linker.ld)
//
// Usage:
//   go run tools/print-kmazarin-addr.go

package main

import (
	"fmt"

	"mazzy/cardinal/constants"
)

func main() {
	// Print kmazarin load address in hex format (e.g., "0x41800000")
	fmt.Printf("0x%X\n", constants.KmazarinLoadAddr)
}
