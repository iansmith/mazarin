// sieve6 is a single-threaded prime sieve program for thread scheduling tests.
// It runs as a separate OS thread/process and prints primes as "6:prime".
package main

import (
	"fmt"
	"runtime"

	"mazzy/mazarin/sys"
)

// sieveOfEratosthenes generates all prime numbers up to n.
func sieveOfEratosthenes(n uint64) []uint64 {
	if n < 2 {
		return nil
	}

	isPrime := make([]bool, n+1)
	for i := range isPrime {
		isPrime[i] = true
	}
	isPrime[0] = false
	isPrime[1] = false

	for p := uint64(2); p*p <= n; p++ {
		if isPrime[p] {
			for multiple := p * p; multiple <= n; multiple += p {
				isPrime[multiple] = false
			}
		}
	}

	var primes []uint64
	for i := uint64(2); i <= n; i++ {
		if isPrime[i] {
			primes = append(primes, i)
		}
	}

	return primes
}

// isPrimeSieve checks if n is prime using the Sieve of Eratosthenes.
func isPrimeSieve(n uint64) bool {
	if n < 2 {
		return false
	}

	primes := sieveOfEratosthenes(n)
	if len(primes) == 0 {
		return false
	}

	return primes[len(primes)-1] == n
}

// asyncHandler is the goroutine that receives kernel async messages.
func asyncHandler() {
	var bundle sys.KernelAsyncBundle
	for {
		if err := sys.WaitKernelAsync(&bundle); err != nil {
			fmt.Printf("[sieve6] WaitKernelAsync failed: %v, exiting async handler\n", err)
			return
		}
		fmt.Printf("[sieve6] Async: op=%d chan=%d msg=[%d,%d,%d,%d]\n",
			bundle.Op, bundle.ChannelId,
			bundle.Message[0], bundle.Message[1],
			bundle.Message[2], bundle.Message[3])
	}
}

func main() {
	// Ensure single-threaded execution
	runtime.GOMAXPROCS(1)

	// Register asyncPreempt for goroutine preemption (still needed for Go runtime)
	if err := sys.RegisterAsyncPreempt(); err != nil {
		fmt.Printf("[sieve6] WARNING: RegisterAsyncPreempt failed: %v\n", err)
	}

	fmt.Println("[sieve6] Starting single-threaded prime sieve")
	fmt.Printf("[sieve6] GOMAXPROCS=%d\n", runtime.GOMAXPROCS(0))

	// Start async handler goroutine
	// go asyncHandler()

	// Run sieve starting at 20001, printing primes as "6:prime"
	for n := uint64(20001); ; n += 2 {
		if isPrimeSieve(n) {
			fmt.Printf("6:%d\n", n)
		}
	}
}
