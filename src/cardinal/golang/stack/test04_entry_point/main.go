package main

import (
	"fmt"
	"os"
)

func main() {
	// Print command-line arguments
	fmt.Printf("=== Command-Line Arguments ===\n")
	fmt.Printf("len(os.Args) = %d\n", len(os.Args))
	for i, arg := range os.Args {
		fmt.Printf("os.Args[%d] = %q\n", i, arg)
	}

	// Print environment variables
	fmt.Printf("\n=== Environment Variables ===\n")
	env := os.Environ()
	fmt.Printf("len(os.Environ()) = %d\n", len(env))
	for i, e := range env {
		fmt.Printf("env[%d] = %q\n", i, e)
	}

	// Show how to access auxv (auxiliary vector)
	fmt.Printf("\n=== Note ===\n")
	fmt.Printf("The auxiliary vector (auxv) is not directly accessible from Go,\n")
	fmt.Printf("but it's passed on the stack after envp. The Go runtime reads it\n")
	fmt.Printf("during initialization to get AT_PAGESZ, AT_RANDOM, etc.\n")
}
