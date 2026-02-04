---
layout: default
title: mazarin news
author: iansmith
---

# News

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
