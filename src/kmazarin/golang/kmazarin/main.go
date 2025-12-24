package main

import (
	"fmt"
	"os"
	"time"
)

// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
//
// Modified to test preemption: both g1 and g2 busy-wait concurrently
// We should see '1' and '2' characters interleaved as scheduler switches between them
func simpleMain() {
	fmt.Println("\r\n[g1] Simple main started!")

	// Test VirtIO RNG
	fmt.Println("[g1] Testing VirtIO RNG by reading from /dev/random...")
	f, err := os.Open("/dev/random")
	if err != nil {
		fmt.Printf("[g1] ERROR: Failed to open /dev/random: %v\r\n", err)
	} else {
		buf := make([]byte, 16)
		n, err := f.Read(buf)
		if err != nil {
			fmt.Printf("[g1] ERROR: Failed to read from /dev/random: %v\r\n", err)
		} else {
			fmt.Printf("[g1] Read %d random bytes: ", n)
			for i := 0; i < n; i++ {
				fmt.Printf("%02x ", buf[i])
			}
			fmt.Println()
		}
		f.Close()
	}

	fmt.Println("[g1] Testing scheduler preemption with two busy-wait goroutines...")

	// Launch g2 - it will busy-wait printing '2'
	fmt.Println("[g1] Launching g2...")
	go simpleGoroutine2(nil)
	fmt.Println("[g1] g2 launched (runtime.newproc called)")

	fmt.Println("[g1] Both goroutines will busy-wait WITHOUT yielding")
	fmt.Println("[g1] If timer-based preemption works, we should see '1' and '2' interleaved")
	fmt.Println("[g1] Starting busy-wait loop (NO cooperative yielding)...\r\n")

	// Infinite busy-wait loop, printing '1' periodically
	// NO calls to Gosched() - relies purely on timer-based preemption
	counter := uint64(0)

	for {
		counter++
		// Every million iterations, print our marker
		if counter%10000000 == 0 {
			// Print '1' to show g1 is running
			fmt.Print("1")
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

// simpleGoroutine2 is the second goroutine for the preemption test
// Pure busy-wait with NO cooperative yielding
func simpleGoroutine2(ch chan string) {
	fmt.Println("[g2] Started, entering busy-wait loop (NO yielding)...")

	// Give g1 a moment to start its loop
	time.Sleep(100 * time.Millisecond)

	// Infinite busy-wait loop to test timer-based preemption
	// NO calls to Gosched() - the timer interrupt must forcibly preempt us
	counter := uint64(0)

	for {
		counter++
		// Every million iterations, print our marker
		if counter%10000000 == 0 {
			// Print '2' to show g2 is running
			fmt.Print("2")
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

func main() {
	simpleMain()
}
