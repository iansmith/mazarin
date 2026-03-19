// shepherd2 is a test program that spawns two goroutines:
// - goroutine 1 prints '1's
// - goroutine 2 prints '2's
// This tests goroutine scheduling within a userspace process.
package main

import (
	"fmt"
)

// busyLoop1 prints '1' in a tight loop to test goroutine scheduling
func busyLoop1() {
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				fmt.Print("\n")
			} else {
				fmt.Print("1")
			}
		}
	}
}

// busyLoop2 prints '2' in a tight loop to test goroutine scheduling
func busyLoop2() {
	counter := uint64(0)
	printCount := uint64(0)

	for {
		counter++
		// Every 100000 iterations, print our marker
		if counter%100000 == 0 {
			printCount++
			if printCount%72 == 0 {
				fmt.Print("\n")
			} else {
				fmt.Print("2")
			}
		}
	}
}

func main() {
	fmt.Println("========================================")
	fmt.Println("[SHEPHERD2] Goroutine scheduling test")
	fmt.Println("========================================")
	fmt.Println("Spawning two goroutines:")
	fmt.Println("  - Goroutine 1: prints 1s")
	fmt.Println("  - Goroutine 2: prints 2s")
	fmt.Println("Starting busy loops...")

	// Launch goroutine 2
	go busyLoop2()

	// Run goroutine 1 in main thread
	busyLoop1()
}
