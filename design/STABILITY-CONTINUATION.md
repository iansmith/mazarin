# Continuation Prompt for Stability Investigation

## Context

A 90-second stability test across all 4 platforms (ARM64 TCG, ARM64 HVF, x86_64, RISC-V) revealed three unexplained anomalies. The full investigation plan is in `design/STABILITY-INVESTIGATION.md`. The test was run using `/stability-test` (defined in `.claude/commands/stability-test.md`).

All platforms boot successfully, load fs.maz, launch all 3 priests (dapope, stdio, sievetest), and run stably for 90 seconds with no crashes. The issues are about metric discrepancies.

## What to investigate

### Issue 2: RISC-V 2x syscall throughput (svc=90K vs ~40K)

Start here — it's the most concrete and actionable.

1. Read `kmazarin/kmazarin/threads.go` around line 1058-1065 to understand how the `svc=` field is reported. Is it cumulative or per-period? The earlier x86_64 test showed non-monotonic values (49148 → 91108 → 66955 → 22756) which is impossible for a cumulative counter that's never reset. This is itself a bug worth understanding.

2. Read `kmazarin/ksyscall/dispatch.go` to see exactly where `TotalSVCCount` is incremented and whether there's any platform-specific path.

3. Check timer frequency: read the timer setup code for each architecture. If RISC-V's timer fires at a different rate, the event dump period differs. Look at:
   - `kmazarin/kirq/` for timer initialization
   - The `cnt%1000` check in threads.go that gates the event dump — if the timer fires at different rates, 1000 ticks means different wallclock time

4. Compare `fmt.Printf` syscall overhead: on RISC-V, does sievetest's printf go through more write() calls? Check buffer sizes in the userspace overlay's write implementation.

### Issue 3: RISC-V kernel heap growth

1. Read `kmazarin/kmem/buddy.go` around the warning at line 297. Understand the page accounting categories (kern, kheap, kpt, boot, user, type).

2. Read `kmazarin/kmem/paging_riscv64.go` — specifically `initProcessL0()` and `VerifyCurrentSATPL3E0()`. Does the latter allocate anything?

3. Compare page table allocation between platforms. RISC-V Sv39 may require more intermediate PT pages than ARM64's TTBR0-based layout.

4. The `kheap` field in the warning is ~40K pages on RISC-V. This is Go runtime heap pages allocated by the kernel. With GOMEMLIMIT=64MiB, the GC should keep heap under 64MB. 40K pages = 160MB far exceeds this. Either GOMEMLIMIT isn't working, or `kheap` counts something other than Go heap.

### Issue 4: ARM64 TCG 89 GC cycles

1. Parse the saved stability test logs in `/tmp/stability-test-*.log` to separate GC lines by their `@` timestamp. Kernel GC has a single continuous timeline. Each priest restarts at `@0.000s`. This tells you whether the 89 cycles are kernel or sievetest.

2. Check the runtime's gctrace write path. In the patched runtime, `write1` (used by gctrace) might be implemented differently than the `write()` syscall. Look at runtime overlay files for `write1` or `write` implementations.

## Key files

- `kmazarin/kmazarin/threads.go` — event dump reporting (~line 1058)
- `kmazarin/ksyscall/dispatch.go` — syscall dispatch, TotalSVCCount increment
- `kmazarin/ksyscall/futex.go` — TotalSVCCount declaration
- `kmazarin/kmem/buddy.go` — kernel memory budget warning
- `kmazarin/kmem/paging_riscv64.go` — RISC-V page table init, SATP verification
- `kmazarin/kmem/paging_arm64.go` — ARM64 page table init (no-op stubs)
- `kmazarin/kmem/paging_amd64.go` — x86_64 page table init
- `kmazarin/kirq/` — timer IRQ setup per architecture
- `kmazarin/ksyscall/write.go` — userspace write() syscall, stderr always echoes to serial
- `kmazarin/kmazarin/main.go` — kernel env setup (~line 720), timer IRQ handler (~line 55)
- `kmazarin/ksyscall/launch.go` — userspace GOGC=5 setting (~line 743)

## Approach

Don't try to fix anything yet. The goal is to understand root causes. For each issue, form a hypothesis, find evidence in the code or logs, and either confirm or refute it. Report findings to the user before making changes — these could be architectural issues that need discussion first.
