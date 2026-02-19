# AMD64 Port — Continuation Prompt 2

## Date: 2026-02-18

## How to Use This Document

Read this ENTIRE document before making any changes. It describes the current state
of the AMD64 port, two active crash bugs, diagnostic code already in the codebase,
and the next investigation steps. Follow the steps in "Next Steps" section at the bottom.

---

## Current State — What Works

The AMD64 (x86_64) kmazarin kernel boots successfully via diplomat UEFI:
- VirtIO GPU framebuffer is visible (1920x1080)
- VirtIO Block device works (FAT32 mount, file reads)
- VirtIO Input devices initialized (keyboard, mouse)
- Timer IRQ fires (LAPIC timer at ~100Hz)
- Two userspace programs are launched: dapope.elf and stdio.elf
- Both programs start executing (print startup messages to serial)
- Context switching between kernel and userspace works (basic preemption)
- Syscalls work (clone, mmap, gettime, etc.)

## Current State — What Doesn't Work

1. **The system crashes ~0.4-1.3 seconds after userspace programs start**
2. The clock display never updates (dapope's timer goroutine gets ~30 timer ticks then crashes)
3. The stdio window never fully renders (gg.NewContext never completes)
4. The eventPoller goroutine never gets CPU time after KernelIdleLoop starts

**Root cause**: Two kernel-mode crashes halt the system. Fixing these should unblock
everything else (clock, stdio, eventPoller).

---

## Bug #1: Page Fault at RIP=0x10282 (Demand Paging NX Bug)

### Symptoms

```
F:0000000000010282@0000000000010282CS=08
G=FFFF800248002380 BP=FFFF800248060EC0 SP=FFFF800248060E80
STK=0000000043A4A790 0000000000000010 00000000438B68EA 0000000000000246
```

- RIP = 0x10282 (NOT a valid code address — both ELF programs start at VA 0x400000)
- CS = 0x08 (kernel mode)
- Page table walk shows: `L3[0x10]=0x8000000077270007` — NX bit SET, page is present

### Root Cause (Partially Identified)

The demand paging handler in `kmazarin/kmem/paging.go:687` maps ALL demand-paged
user pages with `ELF_PF_R | ELF_PF_W` (Read+Write, NO Execute):

```go
// Line 687 in paging.go
elfFlags := uint32(ELF_PF_R | ELF_PF_W) // Read + Write — MISSING ELF_PF_X!
```

This means `makeUserPagePTE` (paging_amd64.go:170) sets the NX bit on every
demand-paged page. On AMD64, the NX bit is hardware-enforced. Any attempt to
execute from an NX page causes a #PF.

### Why This Causes a Loop

1. Something causes execution to jump to VA 0x10282 (not in any ELF segment)
2. Page fault fires (page not present initially)
3. `HandleUserPageFault` checks: `0x10282 >= 0x10000` (minUserAddr) → accepts
4. Demand pager allocates a frame, maps it with NX (no execute)
5. Handler returns "handled=1", exception_return does IRETQ back to 0x10282
6. CPU tries to execute → NX fault → handler sees page already mapped → TLB invalidate → "handled=1"
7. Repeat until the repeat-fault detector (10 iterations) halts

### Stack Trace at Crash

From the diagnostic output:
- `STK[0]` = 0x43A4A790 → **`kmem.mapUserPageWithL0`** (demand paging code)
- `STK[2]` = 0x438B68EA → **`main.KernelIdleLoop`** at offset +0xCA

This suggests KernelIdleLoop's call chain led to the fault at 0x10282.

### What's Still Unknown

**WHY is the CPU executing at 0x10282?** Address 0x10282 is not in any ELF
segment (both programs start at 0x400000). Something must be jumping/returning
to 0x10282. Possibilities:
- Corrupted return address on the goroutine stack
- Corrupted function pointer or Go interface dispatch
- A RET popping 0x10282 from a corrupted stack frame

The stack trace suggests the crash happens during or after demand paging code
within KernelIdleLoop's execution path (ProcessDeadlines or related calls).

---

## Bug #2: Page Fault at RIP=0x8 (Nil Dispatch)

### Symptoms (from earlier runs)

```
F:0000000000000008@0000000000000008CS=08
RA=0000000000000096 SP=FFFF800248060E78 EC=10
```

- RIP = 0x8 (near-null address)
- CS = 0x08 (kernel mode)
- EC = 0x10 = bit 4 set = instruction fetch, bit 0 clear = not present
- RA = 0x96 (value at *RSP — also garbage)
- SP = 0xFFFF800248060E78 (Go goroutine stack in kernel heap)

### Analysis

- Error code 0x10: instruction fetch to a not-present page in supervisor mode
- The value at the top of the faulting stack (0x96) is also garbage
- This suggests **stack corruption** — a RET popped 0x8 as a return address
- The stack is full of garbage (0x8, 0x96) rather than legitimate kernel addresses
- This bug appears inconsistently (depends on timing/code layout)

### Relationship to Bug #1

Bugs #1 and #2 may be the **same underlying issue** manifesting at different
addresses depending on timing. Both show:
- Kernel-mode page fault at a bogus low address
- SP on a Go goroutine stack (0xFFFF8002...)
- Crash occurs ~0.4-1.3 seconds after timer goroutine starts
- Call chain involves KernelIdleLoop

---

## ELF Program Layout (for reference)

Both userspace programs (dapope-amd64.elf, stdio-amd64.elf) have:
- Code segment: VA 0x400000 - ~0x4F2000 (RE = Read+Execute)
- Rodata segment: VA ~0x4ED000 - ~0x60B000 (R = Read)
- Data segment: VA ~0x603000 - ~0x665000 (RW = Read+Write)
- Entry point: ~0x475B80

Kmazarin kernel:
- Code range: VA 0x437FF000 - 0x43B64760
- Entry point: 0x43873580
- Loaded at PA 0x77E00000
- Linear map: 0xFFFFFFFF00000000 + PA

---

## Diagnostic Code Currently in the Codebase

### File: `kmazarin/kmazarin/exceptions_amd64.s`

Three diagnostic sections have been added (all should be REMOVED after fixing):

#### 1. Page Fault Handler — RIP < 0x400000 diagnostic (lines ~485-540)

When a page fault occurs with RIP < 0x400000, prints:
```
G=<R14> BP=<RBP> SP=<faulting RSP>
STK=<*SP> <*(SP+8)> <*(SP+16)> <*(SP+24)>
```
Then halts. Uses a helper function `pf_print_hex16` (lines ~1150-1167).

#### 2. Exception Return — RIP < 0x100000 diagnostic (lines ~898-942)

Before IRETQ in `exception_return`, checks if the return RIP is < 0x100000.
If so, prints `!ER=<RIP> V=<vector>` and continues (does NOT halt — lets the
IRETQ happen so the page fault handler can provide more detail).

#### 3. Context Switch — CS validation (lines ~951-990)

In `load_context_and_iretq`, validates that CS from the ThreadContext is either
0x08 (kernel) or 0x1B (user). Prints `!CS=<value>` and halts if invalid.

#### 4. Timer tilde output (line ~506)

Each timer IRQ prints `~` to COM1 for counting timer ticks before crash.

### Helper: `pf_print_hex16` (lines ~1150-1167)

A `TEXT pf_print_hex16(SB), NOSPLIT|NOFRAME, $0` function that prints R15 as
16 hex characters to COM1 (port 0x3F8). Clobbers AX, CX, DX, R13.

---

## Other Debug Code Still in Codebase

From prior sessions (should also be removed eventually):

1. **`kmazarin/ksyscall/set_timer_deadline.go`**: `DebugSetTimerCalls` atomic counter
2. **`kmazarin/ksyscall/gettime.go`**: `DebugGetTimeCalls` atomic counter
3. **`kmazarin/kmazarin/bottom_half.go`**: `eventPoller()` has a 5-second debug print
   with atomic counters (`debugTimerDeadlineFires`, etc.)
4. **`kmazarin/kmazarin/main.go`**: `[Timer] freq=...` debug print
5. **`kmazarin/ktimer/platform_amd64.s`**: PIT calibration `N=/D=` hex dump
6. **`!0DE=...` output**: This is from somewhere in the exception handler (prints
   error code and RIP for unrecognized exceptions). Address 0x46ABE0 corresponds
   to `runtime.findObject` in the USERSPACE ELF, so this is a user-mode fault that's
   being printed but handled.

---

## Plan for Interrupt-Driven I/O

A plan exists at `/Users/iansmith/.claude/plans/valiant-jingling-perlis.md` for:
1. Adding `ProcessDeadlinesTopHalf` to the AMD64 timer handler (ALREADY DONE)
2. Adding device IRQ dispatch (IOAPIC vectors 32-47)
3. Making `uartTopHalf` platform-specific (COM1 on AMD64)
4. Enabling COM1 RX interrupt + IOAPIC unmask
5. Implementing `platformConfigureDeviceIRQ` for AMD64

**This plan is BLOCKED by the crashes above.** Fix the crashes first.

---

## Build & Run Commands

```bash
# Set environment
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

# Build kmazarin for AMD64
$GO tool task kmazarin-x86_64

# Build and run (50-90s timeout recommended for debugging)
$GO tool task run-x86_64 TIMEOUT=90

# Read serial log safely (NEVER use cat/head/tail directly!)
$GO tool safe-serial-read /tmp/diplomat-serial.log

# Symbol lookup
$GO tool nm build/kmazarin-amd64.elf | grep FunctionName

# Check all 3 architectures still build after changes
$GO tool task kmazarin-arm64
$GO tool task kmazarin-x86_64
$GO tool task kmazarin-riscv64
```

---

## Next Steps (Investigation Priority)

### Step 1: Understand WHY code executes at 0x10282

The faulting stack shows return addresses in `kmem.mapUserPageWithL0` and
`main.KernelIdleLoop`. This is the call chain that leads to the bogus RIP.

**Action**: Disassemble `KernelIdleLoop` around offset +0xCA (address 0x438B68EA)
to see what CALL instruction produces that return address:

```bash
$GO tool objdump -s 'KernelIdleLoop' build/kmazarin-amd64.elf | head -100
```

Also disassemble `mapUserPageWithL0` around the return address:

```bash
$GO tool objdump -s 'mapUserPageWithL0' build/kmazarin-amd64.elf | head -200
```

The goal: identify what function is being called that ends up jumping to 0x10282.

### Step 2: Check if `ProcessDeadlines()` interface dispatch is the culprit

`KernelIdleLoop` calls `ProcessDeadlines()` (line 862 of threads.go) which pops
items from `deadlineQueue` and calls `td.Execute()` → `action.Run()`. This is
interface dispatch — if the interface value is corrupted, it jumps to garbage.

**Action**: Read `kmazarin/util/timer_deadline.go` to understand the `Execute()`
method. Check if `NewWakeThreadAction` (called from `AddDeadline` in threads.go:712)
returns a properly-initialized action. Check if the `deadlineQueue` could contain
corrupted entries.

### Step 3: Add targeted diagnostic for ProcessDeadlines

Add a check in `ProcessDeadlines()` before calling `td.Execute()`:

```go
func ProcessDeadlines() {
    if deadlineQueue == nil {
        return
    }
    currentTick := kirq.ReadCounterValue()
    for !deadlineQueue.IsEmpty() && deadlineQueue.Peek() <= currentTick {
        td := deadlineQueue.Pop()
        // DEBUG: Verify td is valid before interface dispatch
        if td == nil {
            rawUARTPuts("[PD] nil td!\r\n")
            continue
        }
        td.Execute()
    }
}
```

Or better: temporarily replace `ProcessDeadlines()` with an empty function to
see if the crash goes away. If it does, the interface dispatch is the problem.

### Step 4: Check the demand paging NX issue separately

Even after fixing the crash, the demand pager should NOT map all pages as RW
without X. But this is a **secondary issue** — the primary problem is why
execution reaches 0x10282 at all (it's not a valid code address).

However, if you want to fix the NX issue defensively, modify `HandleUserPageFault`
in `kmazarin/kmem/paging.go:687` to include `ELF_PF_X`:

```go
// Line 687: Add execute permission for demand-paged pages
// This is a temporary fix — ideally check the ELF segment permissions
elfFlags := uint32(ELF_PF_R | ELF_PF_W | ELF_PF_X)
```

**WARNING**: This makes ALL demand-paged pages executable (W^X violation). A proper
fix would track which VA ranges are executable from the ELF program headers.

### Step 5: After fixing crashes, implement the interrupt-driven I/O plan

Follow the plan in `/Users/iansmith/.claude/plans/valiant-jingling-perlis.md`.

### Step 6: Clean up all debug code

Remove all diagnostic code listed in the "Diagnostic Code" section above.

---

## Key Files Reference

| File | Purpose |
|------|---------|
| `kmazarin/kmazarin/exceptions_amd64.s` | Exception/IRQ dispatch, SYSCALL entry, IRETQ paths |
| `kmazarin/kmazarin/threads.go` | KernelIdleLoop, ProcessDeadlines, context switching |
| `kmazarin/kmazarin/scheduler.go` | SchedulerFunc, NormalSchedulerFunc |
| `kmazarin/kmem/paging.go` | HandleUserPageFault, demand paging, WalkUserPageTable |
| `kmazarin/kmem/paging_amd64.go` | makeUserPagePTE, NX bit handling |
| `kmazarin/kmazarin/go_abi_macros_amd64.h` | GO_CALL macros for asm→Go calls |
| `kmazarin/kmazarin/abi_stubs_amd64.s` | ABI0 tail-call stubs, YieldToReadyThread |
| `kmazarin/kmazarin/save_context_amd64.go` | SaveContextFromFrame, doContextSwitchABI0 |
| `kmazarin/kmazarin/thread_context_amd64.go` | ThreadContext struct layout |
| `kmazarin/kmazarin/bottom_half.go` | eventPoller, NonTimerIRQTopHalf |
| `kmazarin/util/timer_deadline.go` | TimerDeadline.Execute(), DeadlineAction interface |
| `kmazarin/ksyscall/launch.go` | loadELF, loadSegment, program loading |
| `Taskfile.yml` | Build system, all task definitions |

## AMD64 Exception Frame Layout (for debugging assembly)

After `common_exception_entry` pushes all GPRs:

| Offset | Register | Notes |
|--------|----------|-------|
| 0(SP) | AX | (pushed last) |
| 8(SP) | BX | |
| 16(SP) | CX | |
| 24(SP) | DX | |
| 32(SP) | SI | |
| 40(SP) | DI | |
| 48(SP) | BP | Frame pointer |
| 56(SP) | R8 | |
| 64(SP) | R9 | |
| 72(SP) | R10 | |
| 80(SP) | R11 | |
| 88(SP) | R12 | |
| 96(SP) | R13 | |
| 104(SP) | R14 | Go g register |
| 112(SP) | R15 | |
| 120(SP) | error_code | CPU or ISR stub pushed |
| 128(SP) | RIP | Faulting/return RIP |
| 136(SP) | CS | Code segment |
| 144(SP) | RFLAGS | |
| 152(SP) | RSP | Faulting RSP |
| 160(SP) | SS | Stack segment |

## ThreadContext Layout (AMD64)

| Offset | Field | Notes |
|--------|-------|-------|
| 0 | RAX | |
| 8 | RBX | |
| 16 | RCX | |
| 24 | RDX | |
| 32 | RSI | |
| 40 | RDI | |
| 48 | RBP | |
| 56 | R8 | |
| 64 | R9 | |
| 72 | R10 | |
| 80 | R11 | |
| 88 | R12 | |
| 96 | R13 | |
| 104 | R14 | Go g register |
| 112 | R15 | |
| 120 | RIP | |
| 128 | RFLAGS | |
| 136 | RSP | |
| 144 | FSBase | TLS base |
| 152 | CS | 0x08=kernel, 0x1B=user |
| 160 | SS | 0x10=kernel, 0x23=user |
