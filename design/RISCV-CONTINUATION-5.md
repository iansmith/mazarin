# RISC-V Continuation Prompt #5 — Userspace Execution

## Context

RISC-V kmazarin boots fully: GPU framebuffer, VirtIO block, DTB parsing, timer IRQs, and
context switches all work. Both userspace programs (dapope.elf and stdio.elf) are loaded and
launched successfully. The programs start executing but immediately hit a **store page fault**
when the kernel tries to access user memory.

## Bugs Fixed in Previous Sessions

### 1. readCurrentL0PA() Abstraction (Session 4)
`readTTBR0EL1()` returned raw SATP on RISC-V: PPN not PA. Shared code masked with
`& 0x0000FFFFFFFFFFFF` giving PPN, then added KernelMMIOOffset → unmapped VA. Created
arch-specific `readCurrentL0PA()` in each `paging_{arch}.go` that correctly extracts PA.

### 2. Floating-Point Enable (Session 4)
Userspace FP instructions trapped as illegal instructions (cause 2). RISC-V sstatus.FS
(bits 14:13) was 0 (Off) in userspace context. Fixed:
- `thread_context_riscv64.go:SetupForUserspace()`: sets FS=01 (Initial) in SSTATUS
- `smp_entry_riscv64.s`: secondary harts set FS=01
- `diplomat/main/pagetable_arm64.s`: explicit CPACR_EL1.FPEN for ARM64 primary hart

### 3-5. See RISCV-CONTINUATION-4.md for earlier fixes (SATP format, FDT overwrite,
process page table L3[0] split, identity map L2 conflict, readFileAtRaw FAT chain bug).

## Current Crash

After both programs launch, the kernel hits a **store page fault (cause 15)** in
`SyscallSchedGetaffinity`:

```
!ePF@00000000438AA0F0/?[00007FFF0000DF68>00000000438AA0BC<SP=FFFFFFFE8000BDC8
A1=0000000000002000 N=0000000000000001 G=000000000022D920 C=0000000000000001
```

### Decoding the Crash Output
- `PF` = page fault handler entry (trap handler prints 'P' then falls through)
- `F@438AA0F0` = unhandled fault at SEPC 0x438AA0F0
- `/?` = scause as `'0' + (scause & 0xF)`: `?` = ASCII 0x3F, 0x3F - 0x30 = 0xF = **cause 15 (store page fault)**
- `[00007FFF0000DF68` = STVAL (faulting address in user stack region)
- `>438AA0BC` = RA (return address)

### Faulting Code
```
SyscallSchedGetaffinity (sched_getaffinity.go:50)
  0x438aa0ec  MOV 48(X2), X6           // load user buffer address from stack
  0x438aa0f0  MOV X5, (X6)             // STORE to user address → FAULT
```

The kernel is trying to write the CPU affinity mask to a user-space buffer at
VA `0x00007FFF0000DF68`, which is within the user stack range
(`0x00007FFF00000000 - 0x00007FFF0000FFFF`, allocated by `allocateUserStack`).

## Root Cause Analysis

There are **two issues**, both must be fixed:

### Issue 1: RISC-V SUM Bit (sstatus bit 18) — Primary Cause

On RISC-V, S-mode code **cannot access U-mode pages** when sstatus.SUM = 0. The
RISC-V spec says S-mode load/store to a page with U=1 in the PTE generates a
**page fault** (not access fault) when SUM=0.

- Diplomat's `jumpToKmazarinWithStack` explicitly **clears SUM** (bit 18) in sstatus
- The user stack pages ARE mapped with U=1 (user-accessible)
- When kernel code in `SyscallSchedGetaffinity` stores to user VA → store page fault

**ARM64 comparison**: ARM64 EL1 can access EL0 pages by default when PAN (Privileged
Access Never) is disabled, which is the default on QEMU. No equivalent issue.

**x86_64 comparison**: x86_64 has the SMAP (Supervisor Mode Access Prevention) bit
in CR4, but it defaults to disabled. No equivalent issue.

**Fix needed**: Set SUM=1 when entering syscall/exception handlers so the kernel can
access user memory. Two approaches:
1. Set SUM=1 globally in sstatus during boot (simplest, like Linux does)
2. Set SUM=1 only during syscall handling (more secure)

Recommendation: Set SUM=1 in the trap entry (`trapEntry` in `exceptions_riscv64.s`)
and clear it on trap return. This matches Linux's approach and is the safest pattern.

### Issue 2: Missing HandleUserPageFaultAsm Fallback

The RISC-V page fault handler only calls `HandlePageFaultAsm` (kernel demand-paging).
If that returns 0 (not handled), it prints diagnostics and halts. It does NOT try
`HandleUserPageFaultAsm` as a fallback.

**AMD64 comparison** (`exceptions_amd64.s:360-367`):
```asm
GO_CALL_1_1(·HandlePageFaultAsm, R13)     // Try kernel handler first
TESTQ AX, AX
JNZ   exception_return                     // If handled, return
GO_CALL_1_1(·HandleUserPageFaultAsm, R13)  // Fall through to user handler
```

**ARM64 comparison**: ARM64 has separate exception vectors for EL1 and EL0 syncs,
so it routes to the correct handler at the vector level.

**Fix needed**: After `HandlePageFaultAsm` returns 0 in `exceptions_riscv64.s:385-388`,
try `HandleUserPageFaultAsm` before falling through to `pf_not_handled`. This is needed
for user-space demand paging (mmap'd pages accessed for the first time).

## Files to Modify

### Primary fixes:
- `kmazarin/kmazarin/exceptions_riscv64.s` — Set SUM=1 in trapEntry, clear on return;
  add HandleUserPageFaultAsm fallback in page fault handler

### Verify these work (already correct):
- `kmazarin/ksyscall/sched_getaffinity.go` — Direct user pointer dereference (line 50)
- `kmazarin/ksyscall/launch.go:598-617` — allocateUserStack maps pages with U flag
- `kmazarin/kmem/paging.go:586` — HandleUserPageFault checks valid user VA ranges

## How to Build & Test

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Build and run RISC-V
GOTOOLCHAIN=auto QEMU=$QEMU $GO tool task run-diplomat-riscv64 TIMEOUT=10

# View serial log safely (NEVER cat/Read the raw file)
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log

# Disassemble a function
$GO tool objdump -s 'SyscallSchedGetaffinity' build/kmazarin-riscv64.elf

# Resolve addresses
/opt/homebrew/bin/riscv64-elf-addr2line -e build/kmazarin-riscv64.elf -f <address>

# Test ARM64 for regressions
GOTOOLCHAIN=auto QEMU=$QEMU $GO tool task run-diplomat-arm64 TIMEOUT=8
```

## Success Criteria

1. Both dapope.elf and stdio.elf launch without page faults
2. sched_getaffinity syscall writes to user buffer successfully
3. Userspace programs can execute to the point of making write() or other syscalls
4. ARM64 still works (no regressions from RISC-V changes)

## Exception Handler Output Format Reference

The RISC-V trap handler (`exceptions_riscv64.s`) prints these markers:
- `S<8hex>R<8hex>` — Handled page fault: S=sepc lower 32, R=RA lower 32
- `P` — Page fault handler entry
- `F@<16hex>/<scause_char>[<16hex>` — Unhandled fault: sepc, scause, stval
  - scause_char = `'0' + (scause & 0xF)`, e.g., `?` = 0x3F-0x30 = 0xF = cause 15
- `>RA<SP=...A1=...N=...G=...C=...` — Context dump after unhandled fault
- `X[<16hex>]@<16hex>` — Instruction page fault: stval, sepc (halts)
- `U<digit>@<16hex>[<16hex>` — Unknown exception: scause, sepc, stval
- `e`/`y`/`k`/`f` — Syscall type: ecall/yield/clone/futex
- `!` — Context switch during syscall
- `1`...`5` — SyscallLaunch progress markers
