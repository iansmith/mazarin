// print-kmazarin-addr.go - Print or check kmazarin load address from constants
//
// This tool prints the kmazarin load address computed from the memory layout
// constants defined in cardinal/constants package.
//
// Usage:
//
//	go tool print-kmazarin-addr              # Print address
//	go tool print-kmazarin-addr -check 0x43800000  # Verify address matches
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"mazzy/shared/constants"
)

var flagCheck = flag.String("check", "", "Verify that the constant matches this hex address (exit 1 if mismatch)")

func main() {
	flag.Parse()

	addr := fmt.Sprintf("0x%X", constants.KmazarinLoadAddr)

	if *flagCheck != "" {
		expected := strings.ToUpper(strings.TrimPrefix(*flagCheck, "0x"))
		actual := strings.ToUpper(strings.TrimPrefix(addr, "0x"))
		if expected != actual {
			fmt.Fprintf(os.Stderr, "KMAZARIN_LOAD_ADDR mismatch: Taskfile has 0x%s but constants.KmazarinLoadAddr is 0x%s\n", expected, actual)
			fmt.Fprintf(os.Stderr, "Update KMAZARIN_LOAD_ADDR in Taskfile.yml to %s\n", addr)
			os.Exit(1)
		}
		return
	}

	// Print kmazarin load address in hex format (e.g., "0x41800000")
	fmt.Printf("%s\n", addr)
}
