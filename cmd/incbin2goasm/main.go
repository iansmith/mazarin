// incbin2goasm.go - Convert binary files to Go assembly DATA directives
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	symbolName := flag.String("sym", "binaryData", "Symbol name for the data")
	global := flag.Bool("global", false, "Use global symbols (no · prefix)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-sym name] [-global] input.bin > output.s\n", os.Args[0])
		os.Exit(1)
	}

	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Symbol prefix: · for package-local, empty for global
	prefix := "·"
	if *global {
		prefix = ""
	}

	fmt.Println("#include \"textflag.h\"")
	fmt.Println()
	fmt.Printf("// Auto-generated from %s\n", flag.Arg(0))
	fmt.Printf("// Size: %d bytes\n", len(data))
	fmt.Println()

	// Start symbol
	fmt.Printf("GLOBL %s%s_start(SB), RODATA, $%d\n", prefix, *symbolName, len(data))

	// Data in 8-byte chunks
	for i := 0; i < len(data); i += 8 {
		remaining := len(data) - i
		if remaining >= 8 {
			val := uint64(0)
			for j := 0; j < 8; j++ {
				val |= uint64(data[i+j]) << (j * 8)
			}
			fmt.Printf("DATA %s%s_start+%d(SB)/8, $0x%016x\n", prefix, *symbolName, i, val)
		} else {
			// Handle remaining bytes individually
			for j := 0; j < remaining; j++ {
				fmt.Printf("DATA %s%s_start+%d(SB)/1, $0x%02x\n", prefix, *symbolName, i+j, data[i+j])
			}
		}
	}

	fmt.Println()
	// End symbol - calculate size at runtime by subtracting start from end
	// We need a real symbol, not just a size-0 placeholder, for Go assembly
	// The end is simply start + len(data)
	fmt.Printf("// End symbol for size calculation: end - start = 0x%X\n", len(data))
	// Create a simple function that returns start + size
	fmt.Printf("// KmazarinBinaryEnd should return KmazarinBinaryStart() + 0x%X\n", len(data))
	fmt.Printf("DATA %s%s_size(SB)/8, $0x%016x\n", prefix, *symbolName, len(data))
	fmt.Printf("GLOBL %s%s_size(SB), RODATA, $8\n", prefix, *symbolName)
}
