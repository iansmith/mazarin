# PriestSieve Scheduler Fairness Analysis

## Overview

This report measures CPU scheduling fairness in the Mazzy kernel by running
multiple identical workloads and comparing how much CPU time each receives.
The kernel tracks timer ticks (at 62.5 MHz) per thread and per priest, then
dumps raw counters at shutdown.  A postprocessing step converts these to
human-readable tables and computes fairness metrics.

## What is a Priest (Process)?

In Mazzy's terminology, a **priest** is a userspace process.  Each priest is
a separate ELF binary loaded from a FAT32 disk image by the kernel.  Priests
run at EL0 (unprivileged) and make system calls via SVC to request kernel
services.  A priest can spawn multiple kernel-level threads; the kernel
schedules all threads from all priests on a single CPU core using
timer-driven preemption.

## The PriestSieve Workload

Each priest runs the [`priestsieve`](priestsieve-source.md) program, which:

1. Sets `GOMAXPROCS=1` so all goroutines share a single CPU
2. Spawns a producer goroutine that feeds candidate odd numbers into a
   buffered channel, starting at 20,001 and incrementing by 2
3. Spawns 5 worker goroutines plus the main goroutine as a 6th worker
4. Each worker pulls a candidate number N from the channel and tests
   whether N is prime by running the Sieve of Eratosthenes up to N —
   allocating a boolean array of N+1 elements and marking composites
5. When a worker finds a prime, it prints `"id:prime\n"` to standard
   output via `fmt.Printf`, which triggers an SVC system call to the
   kernel's `write()` handler

The starting value of 20,001 is chosen deliberately.  To test whether
candidate N is prime, the sieve must allocate and iterate over an array of
N+1 booleans.  With candidates in the 20,000+ range, each primality test
involves tens of thousands of loop iterations in pure userspace computation.
This gives the kernel's timer interrupt ample opportunity to preempt the
running thread mid-computation.  Smaller candidates (e.g., under 1,000)
would complete too quickly, spending most of their time in system calls
rather than in preemptible userspace code.

The workload is a mix of computation and I/O: workers spend most of their
time in the sieve's inner loops (userspace), but each prime discovery
triggers a `write()` system call to output the result on the serial port.
The kernel must handle these SVC traps, write to the UART, and return to
userspace — all of which is interleaved with timer-driven preemption.
Channel operations (`candidates <- n` and `<-candidates`) also produce
system calls when goroutines block and unblock.

Each priest instance creates 3 kernel threads (the Go runtime's m0, the
sysmon thread, and the main execution thread).  The vast majority of ticks
accumulate on the main execution thread.

## Test Configuration

| Parameter | Value |
|-----------|-------|
| Priests (processes) | 6 worker priests + 1 kernel (P00) |
| Threads per priest | 3 (Go runtime overhead) |
| Total threads | 21 |
| Timer frequency | 62,500,000 Hz |
| Run duration | 60 seconds |
| Platform | QEMU virt, cortex-a72, 1 CPU core |

## Results

### Per-Thread (Process) Tick Distribution

| Thread | Priest (Process) | Ticks | Seconds |
|-------:|-----------------:|--------------------:|--------:|
| 0 | 0 | 1,170,563 | 0.019 |
| 1 | 0 | 0 | 0.000 |
| 2 | 0 | 0 | 0.000 |
| 60 | 2 | 620,345,561 | 9.926 |
| 15 | 2 | 639,185 | 0.010 |
| 40 | 2 | 55,376 | 0.001 |
| 62 | 14 | 631,240,998 | 10.100 |
| 42 | 14 | 443,309 | 0.007 |
| 27 | 14 | 49,313 | 0.001 |
| 55 | 18 | 621,988,691 | 9.952 |
| 9 | 18 | 393,813 | 0.006 |
| 13 | 18 | 52,938 | 0.001 |
| 49 | 20 | 632,012,127 | 10.112 |
| 51 | 20 | 246,501 | 0.004 |
| 59 | 20 | 50,625 | 0.001 |
| 53 | 26 | 618,146,376 | 9.890 |
| 41 | 26 | 388,249 | 0.006 |
| 25 | 26 | 56,500 | 0.001 |
| 30 | 30 | 622,289,563 | 9.957 |
| 46 | 30 | 571,624 | 0.009 |
| 28 | 30 | 58,938 | 0.001 |
| **SUM** | | **3,750,200,250** | **60.003** |

### Per-Priest (Process) Summary

| Priest (Process) | Ticks | Seconds | Share |
|-----------------:|--------------------:|--------:|------:|
| 20 | 632,309,253 | 10.117 | 16.87% |
| 14 | 631,733,620 | 10.108 | 16.85% |
| 30 | 622,920,125 | 9.967 | 16.62% |
| 18 | 622,435,442 | 9.959 | 16.60% |
| 2 | 621,040,122 | 9.937 | 16.57% |
| 26 | 618,591,125 | 9.897 | 16.50% |
| **Workers** | **3,749,029,687** | **59.984** | |
| Kernel (P00) | 1,170,563 | 0.019 | |
| **TOTAL** | **3,750,200,250** | **60.003** | |

### Kernel Friction

"Friction" measures how much wall-clock time the kernel consumed for its own
overhead (scheduling, exception handling, syscall dispatch) versus time
spent running user workloads.

| Metric | Value |
|--------|------:|
| Wall clock | 60.000 s |
| Worker time | 59.984 s |
| Kernel (P00) time | 0.019 s |
| Accounted total | 60.003 s |
| Unaccounted | -0.003 s |
| Kernel fraction | 0.031% |
| Unaccounted fraction | -0.005% |

The accounted total slightly exceeds the wall clock (by 0.003s) because the
shutdown check fires a few ticks past the 60-second threshold.  The kernel
itself consumed only 0.031% of CPU time — effectively zero overhead for
scheduling 6 processes with 21 total threads on a single core.

### Jain's Fairness Index

Jain's fairness index measures how evenly a resource is distributed:

```
J = (Σxi)² / (n × Σxi²)
```

where xi is the tick count for worker priest i and n = 6.

| Metric | Value |
|--------|------:|
| n | 6 |
| **Jain index** | **0.999929** |

A value of 1.0 indicates perfect fairness (all workers received identical
CPU time).  The measured value of 0.999929 shows near-perfect fairness
across all 6 worker priests over the 60-second run.  The spread between
the most-scheduled priest (10.117s) and the least-scheduled (9.897s) is
only 0.22 seconds, or about 2.2% relative difference.

## Conclusions

1. **Near-perfect fairness**: Jain index of 0.9999 across 6 concurrent
   processes running identical compute-bound workloads.
2. **Negligible kernel overhead**: The kernel consumed 0.031% of total CPU
   time for scheduling, exception handling, and syscall dispatch.
3. **Accurate accounting**: The sum of all per-thread ticks matches the
   wall-clock duration to within 3 milliseconds.
4. **Consistent per-priest structure**: Each priest creates exactly 3 kernel
   threads, with >99.9% of ticks on the main execution thread and minimal
   overhead on the Go runtime's auxiliary threads.
