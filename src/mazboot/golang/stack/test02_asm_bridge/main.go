package main

import "fmt"

// AsmBridge is implemented in bridge.s
// It receives parameters from Go, does some assembly work,
// calls back into a Go helper function, and returns results
func AsmBridge(a int64, b int64, msg string) (result int64, tag string)

func main() {
	fmt.Println("Testing Go → Assembly → Go bridge")

	// Call assembly function with multiple parameters
	result, tag := AsmBridge(100, 42, "hello-from-go")

	fmt.Printf("Result: %d, Tag: %s\n", result, tag)
}

// GoHelper is called BY the assembly function
// It takes multiple parameters and returns multiple values
func GoHelper(x int64, y int64, s1 string, s2 string) (sum int64, combined string) {
	fmt.Printf("GoHelper called: x=%d, y=%d, s1=%s, s2=%s\n", x, y, s1, s2)

	sum = x + y
	combined = s1 + "+" + s2

	return sum, combined
}
