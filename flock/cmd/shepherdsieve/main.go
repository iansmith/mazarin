
// shepherdsieve is a prime number sieve test program for Mazzy userspace.
// It spawns multiple goroutines that cooperatively find primes using
// the Sieve of Eratosthenes, testing goroutine scheduling and preemption.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	"mazzy/mazarin/sys"
)

// candidates is the global channel for distributing prime candidates to workers.
// The producer pushes odd numbers, workers pull and test for primality.
// Buffered channel (size 10) for fairer distribution among workers.
var candidates = make(chan uint64, 10)

// shepherdNumber is this shepherd's index, read from os.Args[1]
var shepherdNumber int

// attemptCounts tracks how many candidates each worker goroutine has received.
// Indexed by worker ID (0-9). Accessed atomically since workers and producer
// run on different goroutines.
var attemptCounts [10]atomic.Uint64

// ShepherdSyscallEntry is the entry point for syscalls from other programs.
// This function's address is patched into userspace programs at load time.
// The symbol name in the ELF will be "main.ShepherdSyscallEntry".
//
//go:noinline
func ShepherdSyscallEntry(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// For now, just print the syscall info
	fmt.Printf("[shepherdsieve] syscall %d (0x%x) args: %x %x %x %x %x %x\n",
		num, num, a1, a2, a3, a4, a5, a6)

	// Check if it's a Mazzy syscall (0x1000+)
	if num >= 0x1000 {
		return handleMazzySyscall(num, a1, a2, a3, a4, a5, a6)
	}

	// Linux syscall - for now, just print and return error
	fmt.Printf("[shepherdsieve] Linux syscall %d not implemented\n", num)
	return -38 // ENOSYS
}

// handleMazzySyscall handles Mazzy-specific syscalls by making real SVC traps
func handleMazzySyscall(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// For Mazzy syscalls, we make the real syscall to kmazarin
	// This uses the standard Go syscall which does SVC
	r1, _, errno := sys.RawSyscall(num, a1, a2, a3, a4, a5, a6)
	if errno != 0 {
		return -int64(errno)
	}
	return int64(r1)
}

// shepherdSyscallEntryAddr holds the address of ShepherdSyscallEntry.
// This is used to prevent the linker from stripping the function
// and to provide a known symbol for the patchshepherd tool to find.
var shepherdSyscallEntryAddr = ShepherdSyscallEntry

// sieveOfEratosthenes generates all prime numbers up to n using the
// classic Sieve of Eratosthenes algorithm.
// Returns a slice of all primes <= n.
func sieveOfEratosthenes(n uint64) []uint64 {
	if n < 2 {
		return nil
	}

	// Create a boolean slice where isPrime[i] indicates if i is prime
	// Initially assume all numbers are prime
	isPrime := make([]bool, n+1)
	for i := range isPrime {
		isPrime[i] = true
	}
	isPrime[0] = false
	isPrime[1] = false

	// Sieve: mark multiples of each prime as composite
	for p := uint64(2); p*p <= n; p++ {
		if isPrime[p] {
			// Mark all multiples of p starting from p*p
			for multiple := p * p; multiple <= n; multiple += p {
				isPrime[multiple] = false
			}
		}
	}

	// Collect all primes into result slice
	var primes []uint64
	for i := uint64(2); i <= n; i++ {
		if isPrime[i] {
			primes = append(primes, i)
		}
	}

	return primes
}

// isPrimeSieve checks if n is prime using the Sieve of Eratosthenes.
// This generates all primes up to n and checks if n is in that set.
func isPrimeSieve(n uint64) bool {
	if n < 2 {
		return false
	}

	primes := sieveOfEratosthenes(n)
	if len(primes) == 0 {
		return false
	}

	// The last prime in the sieve will be <= n
	// If n is prime, it will be the last element (or somewhere in the list)
	return primes[len(primes)-1] == n
}

// primeWorker is a generic worker that reads candidates and prints "id:prime"
func primeWorker(id int) {
	for n := range candidates {
		attemptCounts[id].Add(1)
		if isPrimeSieve(n) {
			fmt.Printf("%d:%d\n", id, n)
		}
		runtime.Gosched()
	}
}

// candidateProducer pushes odd numbers starting from 20001 into the candidates channel.
// Starting at a larger number means the sieve takes longer, giving more compute time
// in userspace rather than syscalls, which helps test preemption.
// 20K provides good balance between computation time and throughput.
// Every 10 candidates sent, it prints per-goroutine attempt counts.
func candidateProducer() {
	sent := uint64(0)
	workerIDs := []int{3, 5, 6, 7, 8, 9}
	for n := uint64(20001); ; n += 2 {
		candidates <- n
		sent++
		if sent%10 == 0 {
			for _, id := range workerIDs {
				fmt.Printf("ATTEMPTS, %d, %d, %d\n", shepherdNumber, id, attemptCounts[id].Load())
			}
		}
	}
}

func main() {
	// Read shepherd number from argv
	if len(os.Args) > 1 {
		var err error
		shepherdNumber, err = strconv.Atoi(os.Args[1])
		if err != nil {
			shepherdNumber = -1
		}
	} else {
		shepherdNumber = -1
	}
	fmt.Printf("[shepherdsieve] shepherd=%d os.Args=%v\n", shepherdNumber, os.Args)

	// Set GOMAXPROCS to ensure goroutine scheduling works
	oldProcs := runtime.GOMAXPROCS(1)
	fmt.Printf("[shepherdsieve] GOMAXPROCS: was %d, now %d\n", oldProcs, runtime.GOMAXPROCS(0))

	// =====================================================
	// USERSPACE ENTRY POINT
	// =====================================================
	// This is the first code running in EL0 (userspace)!
	// If we see this message, the kernel successfully:
	//   1. Loaded shepherd.elf from FAT32 disk
	//   2. Mapped it into low memory with user permissions
	//   3. Performed ERET to EL0
	//   4. We made an SVC syscall for fmt.Println and it worked!
	fmt.Println("============================================")
	fmt.Println("[SHEPHERDSIEVE] RUNNING IN USERSPACE (EL0)!")
	fmt.Println("============================================")

	// Get framebuffer info via syscall
	if fb, err := sys.GetFramebuffer(); err == nil {
		fmt.Printf("[shepherdsieve] Framebuffer: addr=0x%x %dx%d pitch=%d\n",
			fb.Addr, fb.Width, fb.Height, fb.Pitch)
	} else {
		fmt.Printf("[shepherdsieve] No framebuffer: %v\n", err)
	}

	// Print the address of ShepherdSyscallEntry for debugging
	// This also ensures the function is not stripped
	fmt.Printf("[shepherdsieve] ShepherdSyscallEntry at %p\n", shepherdSyscallEntryAddr)

	// For now, just call GetTime to verify we can make Mazzy syscalls
	ts, err := sys.GetTime()
	if err != nil {
		fmt.Printf("[shepherdsieve] GetTime error: %v\n", err)
	} else {
		fmt.Printf("[shepherdsieve] Current time: %d.%09d\n", ts.Seconds, ts.Nanoseconds)
	}

	fmt.Println("[shepherdsieve] Ready to handle syscalls from userspace programs")

	// Demonstrate Sieve of Eratosthenes
	fmt.Println("[shepherdsieve] Testing Sieve of Eratosthenes...")
	testNumbers := []uint64{2, 3, 4, 7, 10, 13, 17, 20, 23, 97, 100}
	for _, n := range testNumbers {
		if isPrimeSieve(n) {
			fmt.Printf("[shepherdsieve] %d is PRIME\n", n)
		} else {
			fmt.Printf("[shepherdsieve] %d is not prime\n", n)
		}
	}

	// Show first 25 primes using the sieve
	primes := sieveOfEratosthenes(100)
	fmt.Printf("[shepherdsieve] First %d primes (up to 100): ", len(primes))
	for i, p := range primes {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%d", p)
	}
	fmt.Println()

	// Spawn goroutines to cooperatively find primes
	// - candidateProducer: pushes odd numbers 3, 5, 7, 9, ... into channel
	// - primeWorker(N): reads candidates, prints "N:prime" when found
	//
	// With GOMAXPROCS=1, all goroutines share a single OS thread.
	// The Go scheduler multiplexes them cooperatively.
	fmt.Println("[shepherdsieve] Starting prime finding workers...")
	fmt.Printf("[shepherdsieve] GOMAXPROCS=%d (all goroutines share one OS thread)\n", runtime.GOMAXPROCS(0))

	go candidateProducer()
	go primeWorker(5)
	go primeWorker(6)
	go primeWorker(7)
	go primeWorker(8)
	go primeWorker(9)
	fmt.Println("[shepherdsieve] 5 worker goroutines spawned + main goroutine as worker 3...")
	primeWorker(3) // Main goroutine runs as worker 3
}
