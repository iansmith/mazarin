# PriestSieve Source Code

Source: `flock/cmd/priestsieve/main.go`

```go
// priestsieve is a prime number sieve test program for Mazzy userspace.
// It spawns multiple goroutines that cooperatively find primes using
// the Sieve of Eratosthenes, testing goroutine scheduling and preemption.
package main

import (
	"fmt"
	"runtime"

	"mazzy/mazarin/sys"
)

// candidates is the global channel for distributing prime candidates to workers.
// The producer pushes odd numbers, workers pull and test for primality.
// Buffered channel (size 10) for fairer distribution among workers.
var candidates = make(chan uint64, 10)

// PriestSyscallEntry is the entry point for syscalls from other programs.
// This function's address is patched into userspace programs at load time.
// The symbol name in the ELF will be "main.PriestSyscallEntry".
//
//go:noinline
func PriestSyscallEntry(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// For now, just print the syscall info
	fmt.Printf("[priestsieve] syscall %d (0x%x) args: %x %x %x %x %x %x\n",
		num, num, a1, a2, a3, a4, a5, a6)

	// Check if it's a Mazzy syscall (0x1000+)
	if num >= 0x1000 {
		return handleMazzySyscall(num, a1, a2, a3, a4, a5, a6)
	}

	// Linux syscall - for now, just print and return error
	fmt.Printf("[priestsieve] Linux syscall %d not implemented\n", num)
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

// priestSyscallEntryAddr holds the address of PriestSyscallEntry.
// This is used to prevent the linker from stripping the function
// and to provide a known symbol for the patchpriest tool to find.
var priestSyscallEntryAddr = PriestSyscallEntry

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
		if isPrimeSieve(n) {
			fmt.Printf("%d:%d\n", id, n)
		}
	}
}

// candidateProducer pushes odd numbers starting from 20001 into the candidates channel.
// Starting at a larger number means the sieve takes longer, giving more compute time
// in userspace rather than syscalls, which helps test preemption.
// 20K provides good balance between computation time and throughput.
func candidateProducer() {
	for n := uint64(20001); ; n += 2 {
		candidates <- n
	}
}

func main() {
	// Set GOMAXPROCS to ensure goroutine scheduling works
	oldProcs := runtime.GOMAXPROCS(1)
	fmt.Printf("[priestsieve] GOMAXPROCS: was %d, now %d\n", oldProcs, runtime.GOMAXPROCS(0))

	// Register our asyncPreempt address with the kernel FIRST
	// This enables goroutine-level preemption within this process
	if err := sys.RegisterAsyncPreempt(); err != nil {
		fmt.Printf("[priestsieve] WARNING: RegisterAsyncPreempt failed: %v\n", err)
	} else {
		fmt.Println("[priestsieve] AsyncPreempt registered successfully")
	}

	// =====================================================
	// USERSPACE ENTRY POINT
	// =====================================================
	// This is the first code running in EL0 (userspace)!
	// If we see this message, the kernel successfully:
	//   1. Loaded priest.elf from FAT32 disk
	//   2. Mapped it into low memory with user permissions
	//   3. Performed ERET to EL0
	//   4. We made an SVC syscall for fmt.Println and it worked!
	fmt.Println("============================================")
	fmt.Println("[PRIESTSIEVE] RUNNING IN USERSPACE (EL0)!")
	fmt.Println("============================================")

	// Get framebuffer info via syscall
	if fb, err := sys.GetFramebuffer(); err == nil {
		fmt.Printf("[priestsieve] Framebuffer: addr=0x%x %dx%d pitch=%d\n",
			fb.Addr, fb.Width, fb.Height, fb.Pitch)
	} else {
		fmt.Printf("[priestsieve] No framebuffer: %v\n", err)
	}

	// Print the address of PriestSyscallEntry for debugging
	// This also ensures the function is not stripped
	fmt.Printf("[priestsieve] PriestSyscallEntry at %p\n", priestSyscallEntryAddr)

	// For now, just call GetTime to verify we can make Mazzy syscalls
	ts, err := sys.GetTime()
	if err != nil {
		fmt.Printf("[priestsieve] GetTime error: %v\n", err)
	} else {
		fmt.Printf("[priestsieve] Current time: %d.%09d\n", ts.Seconds, ts.Nanoseconds)
	}

	fmt.Println("[priestsieve] Ready to handle syscalls from userspace programs")

	// Demonstrate Sieve of Eratosthenes
	fmt.Println("[priestsieve] Testing Sieve of Eratosthenes...")
	testNumbers := []uint64{2, 3, 4, 7, 10, 13, 17, 20, 23, 97, 100}
	for _, n := range testNumbers {
		if isPrimeSieve(n) {
			fmt.Printf("[priestsieve] %d is PRIME\n", n)
		} else {
			fmt.Printf("[priestsieve] %d is not prime\n", n)
		}
	}

	// Show first 25 primes using the sieve
	primes := sieveOfEratosthenes(100)
	fmt.Printf("[priestsieve] First %d primes (up to 100): ", len(primes))
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
	fmt.Println("[priestsieve] Starting prime finding workers...")
	fmt.Printf("[priestsieve] GOMAXPROCS=%d (all goroutines share one OS thread)\n", runtime.GOMAXPROCS(0))

	go candidateProducer()
	go primeWorker(5)
	go primeWorker(6)
	go primeWorker(7)
	go primeWorker(8)
	go primeWorker(9)
	fmt.Println("[priestsieve] 5 worker goroutines spawned + main goroutine as worker 3...")
	primeWorker(3) // Main goroutine runs as worker 3
}
```
