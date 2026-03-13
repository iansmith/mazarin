# Stability Test Investigation Plan (2026-03-13)

Three unexplained anomalies from the 90-second stability test across all 4 platforms.

## Issue 2: RISC-V 2x Syscall Throughput

**Observation**: RISC-V reports svc=90,309 vs ARM64 TCG=35,562, ARM64 HVF=44,954, x86_64=40,673.
All platforms run under QEMU TCG (except HVF), so there should not be a 2x difference.

**What we know**:
- `TotalSVCCount` is the reported metric, incremented once per syscall in `ksyscall/dispatch.go:70`
- RISC-V assembly also increments a separate `syscallDiagCount` in `exceptions_riscv64.s:253`, but this is never reported — it's a dead diagnostic
- The counter is cumulative (never reset), though earlier x86_64 dumps showed non-monotonic values which is itself suspicious

**Investigation steps**:
1. **Add per-period delta reporting**: Change the event dump to report `svc=<delta>` instead of (or in addition to) the raw cumulative counter. This eliminates confusion about whether values are cumulative or per-period.
2. **Add per-priest syscall counters**: Track `TotalSVCCount` per PID in `SyscallDispatch` and report in the event dump. This shows whether one specific priest (e.g., sievetest) is generating disproportionate syscalls on RISC-V.
3. **Check timer frequency**: Compare the actual timer tick rate on RISC-V vs other platforms. If RISC-V's timer fires at a different rate, the "per period" window is different.
4. **Check sievetest behavior**: Does sievetest's `fmt.Printf` generate more syscalls on RISC-V? Printf goes through write() which is a syscall. If RISC-V's buffer sizes or flush behavior differ, more write() calls could result.
5. **Check futex behavior**: RISC-V's futex timeout/wake patterns might cause more syscall round-trips. Compare futex-related counters more carefully.
6. **Verify the non-monotonic x86_64 svc values**: The earlier x86_64 45s test showed svc going 49148 → 91108 → 66955 → 22756 (non-monotonic for a cumulative counter). This is a bug — either the counter is being corrupted, or it's reading the wrong memory. Investigate.

## Issue 3: RISC-V Kernel Heap Growth (160MB, exceeds 128MB budget)

**Observation**: Only RISC-V shows `[kmem] WARNING: kernel exceeds 128MB` warnings. kheap grows to ~40,000 pages (~160MB). ARM64 and x86_64 do not trigger this warning.

**What we know**:
- Warning emitted from `kmazarin/kmem/buddy.go:297` (`buddyWarnKernelLimit`)
- RISC-V has `VerifyCurrentSATPL3E0()` running on every timer IRQ (`main.go:57`) — a real function on RISC-V, no-op on ARM64/x86_64
- RISC-V's `initProcessL0()` in `paging_riscv64.go` creates per-process L2 page tables (ARM64's version is a no-op)
- GC IS running on all platforms (confirmed by gctrace output)
- Kernel uses GOGC=100 (default) and GOMEMLIMIT=64MiB

**Investigation steps**:
1. **Add page allocation tracking by type**: The warning shows `kern=0xA500 kheap=0x9DC0 kpt=0x45 boot=0x6FB user=0x15C7 type=0xC`. kheap is the dominant category at ~40K pages. Track WHERE in the code kernel heap pages are allocated — add caller information to `AllocPage(PageKernelHeap, ...)` calls.
2. **Compare page budget breakdown across platforms**: Run all 3 platforms and dump the full page accounting at the end. Is RISC-V allocating more kheap? More kpt? More user pages?
3. **Check VerifyCurrentSATPL3E0 for side effects**: Does it allocate memory? Does it trigger page faults that cause allocations? Read the function carefully.
4. **Check RISC-V page table depth**: RISC-V Sv39 uses 3-level page tables. If intermediate PT pages are allocated more aggressively on RISC-V, that could explain growth. Compare PT page counts (`kpt` field) across platforms.
5. **Check if the 128MB warning is even correct**: Maybe the warning threshold is wrong for RISC-V due to a different base memory layout, and the actual usage is comparable to other platforms.
6. **Profile over time**: Add timestamps to the warning to see if heap growth is steady (leak) or bursty (one-time allocation).

## Issue 4: ARM64 TCG 89 GC Cycles vs 14-18 on Other Platforms

**Observation**: ARM64 TCG serial log contains 89 `gc N @` lines. ARM64 HVF has 14, x86_64 has 17, RISC-V has 18.

**What we know**:
- Kernel uses GOGC=100 (default), GOMEMLIMIT=64MiB
- Userspace priests use GOGC=5 (aggressive)
- GC trace output goes to stderr (fd=2), which is ALWAYS echoed to serial per `write.go:105` — `EnableSoftIRQConsole` does NOT suppress stderr
- If anything, HVF (faster) should produce MORE GC cycles per wallclock second, not fewer
- The stale comment at `main.go:723` says "GOGC=100" for userspace but code sets GOGC=5

**Investigation steps**:
1. **Distinguish kernel vs userspace GC lines**: The `gc N @` lines don't indicate which process produced them. Add PID tagging to GC output, or check the `@` timestamp — kernel GC has one continuous timeline, each priest starts at `@0.000s`.
2. **Count GC lines per process**: Parse the serial logs to separate kernel GC lines (continuous timestamps) from per-priest GC lines (restart at 0). This tells us whether the 89 cycles are kernel GC or sievetest GC.
3. **Check if kernel GC frequency differs by platform**: If the kernel allocates more on ARM64 TCG (maybe due to slower execution changing allocation patterns), it would GC more often.
4. **Check GOMEMLIMIT behavior**: With GOMEMLIMIT=64MiB, if kernel heap approaches 64MB, the GC runs very aggressively. Maybe ARM64 TCG's slower speed causes the kernel to accumulate more garbage before GC kicks in, leading to more frequent GOMEMLIMIT-triggered cycles.
5. **Verify gctrace write path**: Trace exactly how the kernel's runtime `write(2, ...)` for gctrace reaches the serial port. Is it going through `SyscallWrite`? Or through a different path (runtime `write1`)?
6. **Check for output loss**: Maybe HVF/x86_64/RISC-V produce just as many GC lines but some are lost due to UART speed. Check if serial output drops characters at high throughput.
