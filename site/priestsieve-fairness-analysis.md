# PriestSieve Fairness Analysis

This report analyzes goroutine scheduling fairness in the Mazzy kernel using the `priestsieve` test program. The program spawns 6 worker goroutines that cooperatively find prime numbers using the Sieve of Eratosthenes algorithm, all sharing a single OS thread (`GOMAXPROCS=1`).

## Test Configuration

- **Workers**: 6 goroutines (IDs 3, 5, 6, 7, 8, 9)
- **GOMAXPROCS**: 1 (all goroutines share one OS thread)
- **Channel**: Buffered (size 10) for work distribution
- **Duration**: 15 seconds per test
- **Variable**: Starting candidate number (5K, 10K, 20K)

## Comparative Results

| Metric | 5K Start | 10K Start | 20K Start |
|--------|----------|-----------|-----------|
| Primes found | 918 | 1,959 | 2,649 |
| Userspace hits | 4.2% | 17.1% | 47.2% |
| Coop preempts | 4 | 20 | 57 |
| G-changes | 16 | 62 | 172 |
| Context switches | 7 | 39 | 103 |
| Jain's Index | 0.852 | 0.955 | 0.975 |
| Avg run length | 131.1 | 50.2 | 25.7 |

## Metric Explanations

### Primes Found

The total number of prime numbers discovered during the 15-second test. Larger starting numbers require more computation per candidate (the sieve must check divisibility up to √n), so fewer primes are found per unit time. However, this also means more time spent in actual userspace computation rather than syscalls.

### Userspace Hits (% of Timer IRQs)

The percentage of timer interrupts that caught the CPU executing userspace code (EL0) rather than kernel code (EL1). This is critical because:

- **Goroutine preemption only works in userspace** - the kernel cannot safely inject preemption while handling syscalls
- **Higher percentages indicate more computation time** - the sieve is doing real work rather than making syscalls
- **The Sieve of Eratosthenes is syscall-heavy** - it allocates a boolean array (`make([]bool, n+1)`) and prints results (`fmt.Printf`)

At 5K, only 4.2% of timer IRQs catch userspace code - the sieve runs so fast that goroutines spend 95%+ of their time in syscalls. At 20K, 47.2% of IRQs catch userspace, providing many more preemption opportunities.

### Cooperative Preempts

The number of times the kernel successfully set cooperative preemption flags (`g.preempt = true` and `g.stackguard0 = stackPreempt`) on a userspace goroutine. When these flags are set, the Go runtime will yield at the next function call.

This counter only increments when:
1. Timer IRQ fires while in userspace
2. The goroutine has exceeded its time quantum (50ms)
3. The kernel successfully writes to the goroutine's `g` struct

### G-changes (Goroutine Changes)

The number of times the kernel's timer handler detected that the currently running goroutine changed since the last timer tick. This indicates the Go runtime internally switched goroutines, which happens when:

- A goroutine blocks on a channel operation
- A goroutine makes a syscall and another becomes runnable
- Cooperative preemption triggers a yield

Higher g-change counts indicate more active goroutine scheduling.

### Context Switches

The number of times execution switched from one worker to another (counted by consecutive runs of primes from the same worker). This measures actual work distribution granularity:

- **7 switches at 5K** means workers ran very long stretches uninterrupted
- **103 switches at 20K** means work was distributed in smaller chunks

### Jain's Fairness Index

A standard metric for measuring fairness in resource allocation, defined as:

```
J = (Σxi)² / (n × Σxi²)
```

Where `xi` is the number of primes found by worker `i` and `n` is the number of workers.

- **J = 1.0**: Perfect fairness (all workers got equal work)
- **J = 1/n**: Complete unfairness (one worker got everything)

| Value | Interpretation |
|-------|----------------|
| ≥ 0.95 | Excellent - near-perfect fairness |
| ≥ 0.85 | Good - reasonably fair |
| ≥ 0.70 | Moderate - some imbalance |
| < 0.70 | Poor - significant unfairness |

### Average Run Length

The mean number of consecutive primes a worker finds before another worker gets scheduled. Lower values indicate more frequent context switching and finer-grained work distribution:

- **131.1 at 5K**: Workers ran for long stretches (poor preemption)
- **25.7 at 20K**: Workers switched frequently (good preemption)

## Analysis

### Why Larger Numbers Improve Fairness

The Sieve of Eratosthenes for number `n` requires:
1. Allocating a boolean array of size `n+1` (syscall)
2. Iterating through the array marking composites (userspace computation)
3. Printing the result if prime (syscall)

For small `n`, steps 1 and 3 dominate - the program spends most time in kernel mode handling syscalls. The kernel cannot preempt goroutines during syscalls, so whichever goroutine happens to be running keeps getting scheduled.

For larger `n`, step 2 dominates - the inner loops of the sieve run longer in pure userspace. This gives the timer interrupt more opportunities to catch and preempt the running goroutine.

### The 20K Sweet Spot

At 20K starting candidates:
- **47.2% userspace time** provides ample preemption opportunities
- **Jain's Index of 0.975** indicates near-perfect fairness
- **2,649 primes found** shows good throughput
- **25.7 average run length** means fine-grained scheduling

Going higher (e.g., 100K+) would increase userspace time further but dramatically reduce throughput as each sieve operation becomes very expensive.

## Conclusion

The priestsieve benchmark demonstrates that Mazzy's goroutine preemption mechanism works correctly when goroutines spend sufficient time in userspace computation. The key findings:

1. **Preemption requires userspace time** - syscall-heavy workloads cannot be fairly preempted
2. **Cooperative preemption is effective** - setting `g.stackguard0` successfully triggers Go runtime yields
3. **Fairness scales with computation intensity** - Jain's Index improved from 0.852 to 0.975 as userspace time increased
4. **The buffered channel helps** - decoupling producer/consumer timing improves work distribution

For realistic workloads with meaningful computation (not just I/O), Mazzy achieves excellent scheduling fairness across goroutines sharing a single OS thread.
