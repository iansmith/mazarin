---
layout: default
title: mazarin news
author: iansmith
---

# News

## Mar 13, 2026

**Dynamic module loading.** mazarin can now load PIE ELF binaries (.maz files)
into a running priest's address space. The kernel patches call sites at load
time — BL instructions on ARM64, AUIPC+JALR trampolines on RISC-V, near-call
on x86_64 — so that .maz code can call functions in its host priest. Cross-module
interface assertions work via type deduplication: the kernel re-runs Go's
`typelinksinit` and `itabsinit` after registering the new module's metadata.
Stack traces work across module boundaries.

**Filesystem priest and TOML-driven boot.** The boot sequence is now
data-driven. Each architecture has a `kmazarin.toml` config file that
declares bootstrap priests (loaded by the kernel) and application priests
(loaded by the filesystem). The disk priest loads `fs.maz`, which mounts
FAT32 via an injected `BlockDevice` interface, reads the config, and launches
all application priests from ELF files on disk. No more hardcoded launch
sequences.

**Syscall delegation.** Priests can register as handlers for system calls.
When another priest makes a delegated syscall, the kernel forwards the
request (with data pages) to the handler and blocks the caller until the
reply arrives. The stdio priest uses this to handle `write` and `openat` —
any priest's stdout/stderr output is routed to the console display without
the kernel knowing anything about text rendering.

**Interrupt-driven block I/O.** Disk reads use hardware interrupts instead
of polling. On ARM64, this uses MSI-X through the GICv2m; on RISC-V,
INTx through the PLIC; on x86_64, MSI-X through the LAPIC. The CPU halts
between I/O requests and wakes only when the device signals completion.

**Kernel memory stable at 24MB.** Per-type page accounting was added to
the buddy allocator, which immediately identified a leak in the IPC
delegation path: a sizing mismatch between the thread pool (1024 slots)
and the delegation table (512 slots) caused pages for high-numbered
threads to never be freed. With the fix, kernel resident memory holds
steady at 24MB across all platforms during extended runs. The Go runtime
scavenger can now reclaim physical memory via `madvise(MADV_DONTNEED)`.

**Kernel goroutine preemption.** The timer IRQ handler can now preempt
kernel goroutines, not just userspace threads. This prevents any single
goroutine from monopolizing the CPU. On ARM64 under hardware virtualization
(HVF), a guard on `SPSR.M[0]` ensures the timer never preempts exception
handler code, fixing a class of crashes where the saved PC pointed into
kernel exception return paths.

**x86_64 fully working.** The x86_64 port was hanging during early boot
when reading the FAT32 filesystem. The root cause: `bootYieldForIO()` used
`STI; HLT` to wait for block I/O completion, but with the timer disabled
and MSI-X not waking the CPU, it halted forever. Fixed by reading the
VirtIO ISR register via MMIO (matching ARM64 and RISC-V), which forces a
vCPU exit regardless of interrupt state.

**Stability test results (90-second runs):**

| Platform | Syscalls | Heap | Priests |
|----------|----------|------|---------|
| ARM64 TCG | 9.4M | 24MB | 3 + helloworld.maz |
| ARM64 HVF | 720M | 24MB | 3 + helloworld.maz |
| x86_64 | 5.4M (est.) | 24MB | 3 |
| RISC-V | 7.8M | 24MB | 3 + helloworld.maz |

No panics, no memory leaks, GC running in kernel and all priests on every
platform.

## Feb 23, 2026

**Full device support on all three architectures.** mazarin now has working
VirtIO GPU, VirtIO Block, VirtIO Keyboard, Virtio RTC, and VirtIO Mouse drivers on all
three supported architectures: ARM64, x86_64, and RISC-V. Each architecture
boots into a graphical display (1920x1080 framebuffer), reads from a FAT32
disk image, and receives keyboard and mouse input — all through the same
high-level driver code. The userspace programs dapope (clock + input handler)
and stdio (stdout/stderr display server) run successfully on all three
platforms.

**Architecture-independent kernel.** The kernel proper (kmazarin) is now
largely architecture-independent. Architecture-specific code is isolated
into per-arch packages for exception handling, context switching, page
tables, interrupt controllers, and timers. The VirtIO drivers, FAT32
filesystem, scheduler, syscall dispatch, and demand paging are shared across
all three architectures. This means new kernel features written once
automatically work on ARM64, x86_64, and RISC-V.

**Custom RISC-V boot path.** There is currently no working UEFI firmware
for the RISC-V "virt" board in QEMU, so mazarin cannot use the diplomat UEFI
bootloader on RISC-V. Instead, diplomat is loaded directly by OpenSBI using
its `-kernel` flag, bypassing UEFI entirely. Diplomat then handles ELF
loading, page table setup, and the jump to kmazarin. This required a
separate boot path — including allocation-free FAT32 mounting and direct
VirtIO block access during early boot — but the kernel itself is identical
across all architectures once it starts running.

**AMD64 at feature parity.** The x86_64 port has reached full feature parity
with ARM64 and RISC-V. This involved implementing APIC timer interrupts,
IDT exception dispatch, x86_64 context switching with XMM register
save/restore, demand paging via CR2 fault address, and correct GDT/TSS
segment selectors. A particularly subtle bug involved XMM register corruption
during page fault handling — Go's `memmove` uses SSE instructions, and a
page fault during a move would clobber XMM registers in the handler before
the CPU retried the faulting instruction with corrupted data.

## Feb 4, 2026

**Multicore support.** mazarin's scheduler now supports SMP operation with
up to 8 CPU cores. The new scheduler includes per-CPU ready queues with work
stealing for better cache locality, per-thread CPU affinity, and deadline-based
preemption. The 8-core limit is a consequence of using GICv2 (Generic Interrupt
Controller v2), which only supports 8 CPUs via its CPU interface targeting
mechanism. Moving to GICv3 would lift this limit, but GICv3 uses a
fundamentally different programming model (system registers instead of
memory-mapped) and is a future project.

**ARM64 UEFI boot.** mazarin has two bootloaders: cardinal, the bare-metal
bootloader that handles hardware initialization from scratch, and diplomat,
a UEFI bootloader that delegates hardware setup to firmware. diplomat now
has ARM64 UEFI support, alongside the existing x86_64 UEFI path. This
should enable booting mazarin on ARM64 hypervisors that provide UEFI
firmware, including Tart and other macOS-native virtualization solutions.
The UEFI approach is considerably simpler than bare-metal boot (diplomat is
about 4,400 lines vs cardinal's 24,000) because UEFI firmware handles
hardware initialization, memory maps, and provides a standard interface for
boot services.

**Multi-architecture HAL progress.** We have begun extracting
architecture-specific subsystems into clean, per-architecture packages to
support x86_64 and RISC-V alongside the existing ARM64 implementation. The
table below shows the current status of 7 architecture-specific kernel
subsystems.

| # | Subsystem | ARM64 | x86_64 | RISC-V | Status |
|---|-----------|-------|--------|--------|--------|
| 6 | Timer | Reference | TSC Deadline | SBI set_timer | Complete |
| 7 | Spinlocks & Barriers | Reference | LOCK CMPXCHG + PAUSE | LR.W/SC.W + FENCE | Complete |
| 5 | SMP Boot | Exists | -- | -- | Not started |
| 3 | Interrupt Controller | Exists (GICv2) | -- | -- | Not started |
| 2 | Context Switch | Exists | -- | -- | Not started |
| 4 | Page Tables | Exists | -- | -- | Not started |
| 1 | Exception Entry & Dispatch | Exists | -- | -- | Not started |

The x86_64 and RISC-V implementations were produced by autonomous Claude Code
agents working in isolated git worktrees from architecture-specific
instructions. The x86_64 agent produced correct, mergeable code on each
attempt. The RISC-V agent produced structurally correct code with the right
algorithms, but hand-encoded WORD instructions (needed because Go's assembler
does not support all RISC-V instructions natively) had incorrect hex encodings
in 3 of 5 cases. These were caught during review and fixed manually. Pre-computing
the instruction encodings in the agent instructions would likely avoid this
issue in future rounds.

## Jan 26, 2026

As of Jan 26 (77b4f83), the following features are supported with respect to the operating
system proper and all are written in go or go's assembly variant:

* bootloading
* kernel
* userspace programs with protection from each other and the kernel
* multiple threads, currently 3 per go userspace program since that is the go runtime behavior
* simple, fair scheduling of threads including kernel threads
* multiple go routines in a userspace program
* fair scheduling of goroutines within a userspace program
* primitive support for lightweight go programs called mazs (plural)

![Serial console with neumorphic card frame](console-serial.png)

*GUI work is underway.*
