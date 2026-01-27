// priest3 is a test program that spawns three goroutines:
// - goroutine 1 prints '4's
// - goroutine 2 prints '6's
// - goroutine 3 prints '7's
// This tests goroutine scheduling within a userspace process.
package main

import (
	"fmt"
)

// busyLoop4 prints '4' in a tight loop to test goroutine scheduling
func busyLoop4() {
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
				fmt.Print("4")
			}
		}
	}
}

// busyLoop6 prints '6' in a tight loop to test goroutine scheduling
func busyLoop6() {
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
				fmt.Print("6")
			}
		}
	}
}

// busyLoop7 prints '7' in a tight loop to test goroutine scheduling
func busyLoop7() {
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
				fmt.Print("7")
			}
		}
	}
}

func main() {
	fmt.Println("========================================")
	fmt.Println("[PRIEST3] Goroutine scheduling test")
	fmt.Println("========================================")
	fmt.Println("Spawning three goroutines:")
	fmt.Println("  - Goroutine 1: prints 4s")
	fmt.Println("  - Goroutine 2: prints 6s")
	fmt.Println("  - Goroutine 3: prints 7s")
	fmt.Println("Starting busy loops...")

	// Launch goroutines 2 and 3
	go busyLoop6()
	go busyLoop7()

	// Run goroutine 1 in main thread
	busyLoop4()
}
