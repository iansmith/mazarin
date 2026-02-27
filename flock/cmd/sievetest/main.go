// sievetest is a simple prime sieve program for multi-priest thread testing.
// It runs as a single-threaded priest (no extra goroutines) and prints primes
// with its thread ID prefix.
package main

import (
	"fmt"
	"runtime"
	"syscall"
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

func main() {
	// Ensure single-threaded execution
	runtime.GOMAXPROCS(1)

	// Get our thread ID for identification
	tid, _, _ := syscall.Syscall(syscall.SYS_GETTID, 0, 0, 0)

	fmt.Printf("[T%02X] Starting prime sieve at 20001\n", tid)

	// Run sieve starting at 20001, printing primes with TID prefix
	for n := uint64(20001); ; n += 2 {
		if isPrimeSieve(n) {
			fmt.Printf("T%02X:%d\n", tid, n)
		}
	}
}
