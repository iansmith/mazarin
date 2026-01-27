// sieve-fairness analyzes priestsieve output to evaluate goroutine scheduling fairness.
// It reads from stdin (pipe from safe-serial-read) and produces fairness statistics.
//
// Usage:
//
//	$GO tool safe-serial-read | $GO tool sieve-fairness
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// WorkerStats tracks statistics for a single worker
type WorkerStats struct {
	ID         int
	Primes     []uint64 // All primes found by this worker
	RunLengths []int    // Number of primes in each consecutive run
}

// PreemptStats tracks kernel preemption statistics
type PreemptStats struct {
	TimerIRQs         int
	UserspacePathHits int
	CoopPreempts      int
	GChanges          int
}

func main() {
	// Regex patterns
	primePattern := regexp.MustCompile(`^([3-9]):(\d+)$`)
	userspacePattern := regexp.MustCompile(`\[UserspacePreempt\] path=(\d+) coop=(\d+) gchange=(\d+)`)
	timerPattern := regexp.MustCompile(`TimerIRQs=(\d+)`)

	// Worker stats indexed by worker ID
	workers := make(map[int]*WorkerStats)

	// Track consecutive runs
	var lastWorker int = -1
	var currentRunLength int = 0

	// Preemption stats (take the last one)
	var preemptStats PreemptStats

	// Read from stdin
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")

		// Check for prime output (e.g., "5:100019")
		if matches := primePattern.FindStringSubmatch(line); matches != nil {
			workerID, _ := strconv.Atoi(matches[1])
			prime, _ := strconv.ParseUint(matches[2], 10, 64)

			// Get or create worker stats
			ws, ok := workers[workerID]
			if !ok {
				ws = &WorkerStats{ID: workerID}
				workers[workerID] = ws
			}
			ws.Primes = append(ws.Primes, prime)

			// Track run lengths
			if workerID == lastWorker {
				currentRunLength++
			} else {
				// End of previous run
				if lastWorker != -1 && currentRunLength > 0 {
					workers[lastWorker].RunLengths = append(workers[lastWorker].RunLengths, currentRunLength)
				}
				lastWorker = workerID
				currentRunLength = 1
			}
			continue
		}

		// Check for UserspacePreempt stats
		if matches := userspacePattern.FindStringSubmatch(line); matches != nil {
			preemptStats.UserspacePathHits, _ = strconv.Atoi(matches[1])
			preemptStats.CoopPreempts, _ = strconv.Atoi(matches[2])
			preemptStats.GChanges, _ = strconv.Atoi(matches[3])
			continue
		}

		// Check for TimerIRQs
		if matches := timerPattern.FindStringSubmatch(line); matches != nil {
			preemptStats.TimerIRQs, _ = strconv.Atoi(matches[1])
		}
	}

	// Record the final run
	if lastWorker != -1 && currentRunLength > 0 {
		workers[lastWorker].RunLengths = append(workers[lastWorker].RunLengths, currentRunLength)
	}

	if len(workers) == 0 {
		fmt.Println("No prime output found in input")
		os.Exit(1)
	}

	// Sort worker IDs for consistent output
	var workerIDs []int
	for id := range workers {
		workerIDs = append(workerIDs, id)
	}
	sort.Ints(workerIDs)

	// Calculate totals
	totalPrimes := 0
	for _, ws := range workers {
		totalPrimes += len(ws.Primes)
	}

	// Print report
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              PRIESTSIEVE FAIRNESS ANALYSIS REPORT                ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Preemption Statistics
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ KERNEL PREEMPTION STATISTICS                                    │")
	fmt.Println("├─────────────────────────────────────────────────────────────────┤")
	fmt.Printf("│ Timer IRQs:              %6d                                  │\n", preemptStats.TimerIRQs)
	fmt.Printf("│ Userspace path hits:     %6d", preemptStats.UserspacePathHits)
	if preemptStats.TimerIRQs > 0 {
		pct := float64(preemptStats.UserspacePathHits) / float64(preemptStats.TimerIRQs) * 100
		fmt.Printf(" (%5.1f%% of IRQs)              │\n", pct)
	} else {
		fmt.Println("                                  │")
	}
	fmt.Printf("│ Cooperative preempts:    %6d                                  │\n", preemptStats.CoopPreempts)
	fmt.Printf("│ Goroutine changes:       %6d                                  │\n", preemptStats.GChanges)
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Worker Distribution
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ WORKER DISTRIBUTION                                             │")
	fmt.Println("├─────────┬─────────┬──────────┬──────────────────────────────────┤")
	fmt.Println("│ Worker  │ Primes  │ Share    │ Bar                              │")
	fmt.Println("├─────────┼─────────┼──────────┼──────────────────────────────────┤")

	maxBar := 30
	for _, id := range workerIDs {
		ws := workers[id]
		count := len(ws.Primes)
		share := float64(count) / float64(totalPrimes) * 100
		barLen := int(float64(count) / float64(totalPrimes) * float64(maxBar))
		bar := strings.Repeat("█", barLen)
		fmt.Printf("│   %d     │ %6d  │ %5.1f%%   │ %-30s │\n", id, count, share, bar)
	}
	fmt.Printf("├─────────┼─────────┼──────────┼──────────────────────────────────┤\n")
	fmt.Printf("│ Total   │ %6d  │ 100.0%%   │                                  │\n", totalPrimes)
	fmt.Println("└─────────┴─────────┴──────────┴──────────────────────────────────┘")
	fmt.Println()

	// Fairness Metrics
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ FAIRNESS METRICS                                                │")
	fmt.Println("├─────────────────────────────────────────────────────────────────┤")

	// Calculate coefficient of variation (CV) - lower is more fair
	// CV = stddev / mean
	n := len(workers)
	mean := float64(totalPrimes) / float64(n)
	var sumSquares float64
	for _, ws := range workers {
		diff := float64(len(ws.Primes)) - mean
		sumSquares += diff * diff
	}
	stddev := math.Sqrt(sumSquares / float64(n))
	cv := stddev / mean * 100

	// Jain's Fairness Index: sum(xi)^2 / (n * sum(xi^2))
	// Ranges from 1/n (completely unfair) to 1 (perfectly fair)
	var sumX, sumX2 float64
	for _, ws := range workers {
		x := float64(len(ws.Primes))
		sumX += x
		sumX2 += x * x
	}
	jainIndex := (sumX * sumX) / (float64(n) * sumX2)

	// Min/Max ratio
	minPrimes := totalPrimes
	maxPrimes := 0
	for _, ws := range workers {
		count := len(ws.Primes)
		if count < minPrimes {
			minPrimes = count
		}
		if count > maxPrimes {
			maxPrimes = count
		}
	}
	minMaxRatio := float64(minPrimes) / float64(maxPrimes) * 100

	fmt.Printf("│ Expected per worker:     %6d (if perfectly fair)             │\n", totalPrimes/n)
	fmt.Printf("│ Coefficient of Var:      %5.1f%% (lower is better)              │\n", cv)
	fmt.Printf("│ Jain's Fairness Index:   %5.3f  (1.0 = perfect)                │\n", jainIndex)
	fmt.Printf("│ Min/Max Ratio:           %5.1f%% (100%% = perfect)              │\n", minMaxRatio)
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Run Length Analysis
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ RUN LENGTH ANALYSIS (consecutive primes per worker)            │")
	fmt.Println("├─────────┬─────────┬─────────┬─────────┬─────────┬──────────────┤")
	fmt.Println("│ Worker  │  Runs   │   Min   │   Max   │   Avg   │  Std Dev     │")
	fmt.Println("├─────────┼─────────┼─────────┼─────────┼─────────┼──────────────┤")

	var allRunLengths []int
	for _, id := range workerIDs {
		ws := workers[id]
		runs := ws.RunLengths
		allRunLengths = append(allRunLengths, runs...)

		if len(runs) == 0 {
			fmt.Printf("│   %d     │    0    │    -    │    -    │    -    │      -       │\n", id)
			continue
		}

		minRun := runs[0]
		maxRun := runs[0]
		sumRun := 0
		for _, r := range runs {
			if r < minRun {
				minRun = r
			}
			if r > maxRun {
				maxRun = r
			}
			sumRun += r
		}
		avgRun := float64(sumRun) / float64(len(runs))

		var runSumSquares float64
		for _, r := range runs {
			diff := float64(r) - avgRun
			runSumSquares += diff * diff
		}
		runStdDev := math.Sqrt(runSumSquares / float64(len(runs)))

		fmt.Printf("│   %d     │ %6d  │ %6d  │ %6d  │ %6.1f  │ %10.1f   │\n",
			id, len(runs), minRun, maxRun, avgRun, runStdDev)
	}
	fmt.Println("├─────────┼─────────┼─────────┼─────────┼─────────┼──────────────┤")

	// Overall run length stats
	if len(allRunLengths) > 0 {
		minAll := allRunLengths[0]
		maxAll := allRunLengths[0]
		sumAll := 0
		for _, r := range allRunLengths {
			if r < minAll {
				minAll = r
			}
			if r > maxAll {
				maxAll = r
			}
			sumAll += r
		}
		avgAll := float64(sumAll) / float64(len(allRunLengths))
		var allSumSquares float64
		for _, r := range allRunLengths {
			diff := float64(r) - avgAll
			allSumSquares += diff * diff
		}
		allStdDev := math.Sqrt(allSumSquares / float64(len(allRunLengths)))

		fmt.Printf("│ Overall │ %6d  │ %6d  │ %6d  │ %6.1f  │ %10.1f   │\n",
			len(allRunLengths), minAll, maxAll, avgAll, allStdDev)
	}
	fmt.Println("└─────────┴─────────┴─────────┴─────────┴─────────┴──────────────┘")
	fmt.Println()

	// Prime Range
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ PRIME RANGE                                                     │")
	fmt.Println("├─────────────────────────────────────────────────────────────────┤")
	var minPrime, maxPrime uint64 = math.MaxUint64, 0
	for _, ws := range workers {
		for _, p := range ws.Primes {
			if p < minPrime {
				minPrime = p
			}
			if p > maxPrime {
				maxPrime = p
			}
		}
	}
	fmt.Printf("│ Smallest prime found:    %12d                            │\n", minPrime)
	fmt.Printf("│ Largest prime found:     %12d                            │\n", maxPrime)
	fmt.Printf("│ Range tested:            %12d                            │\n", maxPrime-minPrime)
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")

	// Verdict
	fmt.Println()
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ VERDICT                                                         │")
	fmt.Println("├─────────────────────────────────────────────────────────────────┤")
	if jainIndex >= 0.95 {
		fmt.Println("│ ✓ EXCELLENT: Near-perfect fairness (Jain >= 0.95)              │")
	} else if jainIndex >= 0.85 {
		fmt.Println("│ ✓ GOOD: Reasonably fair distribution (Jain >= 0.85)            │")
	} else if jainIndex >= 0.70 {
		fmt.Println("│ △ MODERATE: Some imbalance present (Jain >= 0.70)              │")
	} else {
		fmt.Println("│ ✗ POOR: Significant unfairness (Jain < 0.70)                   │")
	}
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
}
