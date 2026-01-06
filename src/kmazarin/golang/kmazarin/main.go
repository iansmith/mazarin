package main

import (
	"fmt"
	"os"
	"unsafe"
)

// init runs before main - print debug marker to show runtime initialized
func init() {
	// Print "KMAZARIN" to show we reached init
	uartPutc('K')
	uartPutc('M')
	uartPutc('A')
	uartPutc('Z')
	uartPutc('A')
	uartPutc('R')
	uartPutc('I')
	uartPutc('N')
	uartPutc('\r')
	uartPutc('\n')
}

// uartPutc writes a single character directly to UART (bypasses Go runtime)
//go:nosplit
func uartPutc(c byte) {
	const uartBase = uintptr(0x09000000)
	*(*byte)(unsafe.Pointer(uartBase)) = c
}

// readDAIF reads the DAIF register to check interrupt mask state
//go:nosplit
func readDAIF() uint64 {
	var daif uint64
	// MRS DAIF, X0 = 0xD53B4200
	// We'll use inline assembly via pointer tricks
	// For now, just read via a syscall to mazboot (we pass 9999 as a special debug syscall)
	// Actually, let's just use raw assembly in a .s file
	// For now, print a placeholder and check if timer fires at all
	return daif
}

// printHex prints a hex value directly to UART
//go:nosplit
func printHex(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
}

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
		// Every 1000 iterations, print our marker
		if counter%1000 == 0 {
			// Print '1' to show g1 is running (direct UART, no runtime)
			uartPutc('1')
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

// simpleGoroutine2 is the second goroutine for the preemption test
// Pure busy-wait with NO cooperative yielding
func simpleGoroutine2(ch chan string) {
	fmt.Println("[g2] Started, entering busy-wait loop (NO yielding)...")

	// Infinite busy-wait loop to test timer-based preemption
	// NO calls to Gosched() - the timer interrupt must forcibly preempt us
	counter := uint64(0)

	for {
		counter++
		// Every 1000 iterations, print our marker
		if counter%1000 == 0 {
			// Print '2' to show g2 is running (direct UART, no runtime)
			uartPutc('2')
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

func main() {
	simpleMain()
}
