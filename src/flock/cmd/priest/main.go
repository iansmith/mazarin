//go:build qemuvirt && aarch64

// priest is the first userspace program for Mazzy.
// It demonstrates basic goroutine scheduling and syscall usage.
package main

import (
	"fmt"
	"mazarin/sys"
	"time"
)

func main() {
	fmt.Println("[priest] Starting first userspace program")

	// Launch the time-reporting goroutine
	go timeReporter()

	// Main goroutine: use Go's timer mechanism to print dots
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C
		fmt.Printf(".")
	}
}

// timeReporter periodically calls GetTime and prints the result
func timeReporter() {
	// Wait a bit before starting to let main establish its pattern
	time.Sleep(500 * time.Millisecond)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C

		ts, err := sys.GetTime()
		if err != nil {
			fmt.Printf("\n[priest] GetTime error: %v\n", err)
			continue
		}

		fmt.Printf("\n[priest] Time: %d.%09d\n", ts.Seconds, ts.Nanoseconds)
	}
}
