# AMD64 Continuation 1: Userspace Demand Paging Not Working

## Current State

The x86_64 kmazarin kernel boots fully:
- Diplomat loads kmazarin ELF, sets up page tables, GDT, IDT, TSS, SYSCALL MSRs
- VirtIO GPU (1920x1080 framebuffer), Block (257MB), Input (keyboard + mouse) all working
- Timer IRQ, context switching, thread management all working
- dapope.elf and stdio.elf are loaded and launched from FAT32 disk

But both userspace programs immediately crash with page faults:
```
F:0000000080000000@00000000004166DD
F:0000000080040000@000000000041671D
```

The faulting addresses (0x80000000, 0x80040000) are the first pages of the userspace mmap bump region. The userspace programs are making SYSCALL to mmap memory (Go runtime init), getting back 0x80000000+, then faulting when accessing it because demand paging isn't working.

## What Was Fixed This Session

1. **CS/SS selector crash** — `abi_stubs_amd64.s` hardcoded stale UEFI selectors (CS=0x38, SS=0x30) instead of reading from ThreadContext. Fixed `RunFirstThread` and `YieldToReadyThread`. Added `initThread0Context` for all arches.

2. **Dispatch table using ARM64 syscall numbers** — `dispatch.go` was indexed by ARM64 native syscall numbers. AMD64's `clone` (56) dispatched to `SyscallOpenat` (56 on ARM64). Fixed: table now indexed by `SysID`, wired up `translateSyscallNum()` per-arch.

3. **Missing `fds_unix.go` overlay** — Go 1.25's `runtime.checkfds()` calls `fcntl(fd, F_GETFD)` for fds 0-2. Missing overlay caused crash. Added to ARM64 and AMD64 overlays.

4. **Missing `arch_prctl` handler** — `SyscallArchPrctl` existed but wasn't in dispatch table. Userspace `runtime.settls` called `arch_prctl(ARCH_SET_FS)` → got ENOSYS → deliberate crash at address 0xF1. Fixed: added `SysIDArchPrctl: SyscallArchPrctl` to dispatch table.

5. **Unhandled page fault infinite loop** — Assembly PF handler jumped to `exception_return` (IRETQ back to faulting instruction). Fixed: check CS for user vs kernel fault, call `ThreadExitAsm` for user faults.

6. **`main.syscallEntry` not in wanted symbols** — Diplomat couldn't find the symbol to set LSTAR. Kmazarin self-corrected but it's cleaner to resolve at boot. Added to `wantedSymbols` in `elf_loader.go`.

7. **Mmap VA overlap with kmazarin** — `userMmapStart` was 0x10000000 (256MB). Bump allocator grew into kmazarin's PDPT[1] (0x40000000-0x7FFFFFFF). Made `userMmapStart` arch-specific: 0x80000000 on AMD64 (above kmazarin), 0x10000000 on ARM64/RISC-V.

## Root Cause of Current Crash

The `GO_CALL_1_1` macro calls from the page fault assembly handler to Go code (`HandlePageFaultAsm`, `HandleUserPageFaultAsm`) appear to **not execute** — no UART output from either handler, yet the `F:` diagnostic (written by assembly OUTB) prints. This suggests:

1. The `GO_CALL_1_1` macro might have a bug on AMD64 that causes a silent crash/return
2. OR the Go function panics before producing any output
3. OR the UART functions don't work in the page fault context

### Key Observation
The same `GO_CALL` macros work in other contexts:
- `GO_CALL_7_1(·SyscallDispatch, ...)` works (syscalls succeed)
- `GO_CALL_0_1(·GetSyscallSwitchTarget)` works (context switches succeed)
- `GO_CALL_4_0(·TimerIRQHandler, ...)` works (timer ticks)
- `GO_CALL_0_1(·ThreadExitAsm)` works (threads are killed on unhandled PF)

But the page fault handler's Go calls produce no UART output. The difference: the page fault handler runs with different kernel g/FS_BASE state.

## Investigation Steps

1. **Check GO_CALL_1_1 macro definition** in `kmazarin/kmazarin/go_abi_macros_amd64.h` — verify stack frame size and argument passing

2. **Verify kernel g setup in PF handler** — The PF handler (line 252 of `exceptions_amd64.s`) reads FS_BASE and writes kmazarinG0Addr to `TLS_slot = FS_BASE - 8`. But FS_BASE might be 0 (uninitialized) if the first PF happens before any thread has set FS_BASE. If FS_BASE=0, writing to `(0 - 8)` = 0xFFFFFFFFFFFFFFF8 could crash.

3. **Add assembly-level UART diagnostic** in the PF handler BEFORE the Go call — write a single byte via `OUTB` to COM1 (port 0x3F8) right before `GO_CALL_1_1(·HandlePageFaultAsm, R13)` to confirm the handler reaches that point

4. **Check if `HandlePageFault` has nosplit issues** — the function calls `debugPrint`, `InitPaging`, `getKmazarinSize` etc. If the stack is too small for nosplit, it might crash

5. **Compare with ARM64/RISC-V PF handler flow** — ARM64 `exceptions_arm64.s` line 524 and RISC-V `exceptions_riscv64.s` line 368 both use GO_CALL_1_1 for the same purpose. What's different about AMD64?

## Key Files

| File | Role |
|------|------|
| `kmazarin/kmazarin/exceptions_amd64.s:252` | Page fault handler entry (handle_page_fault) |
| `kmazarin/kmazarin/go_abi_macros_amd64.h` | GO_CALL macros for AMD64 |
| `kmazarin/kmem/paging.go:413` | HandlePageFault (kernel demand paging) |
| `kmazarin/kmem/paging.go:618` | HandleUserPageFault (user demand paging) |
| `kmazarin/kmem/paging.go:1316` | mapUserPageWithL0 (creates page table entries) |
| `kmazarin/kmem/paging_amd64.go` | AMD64 page table helpers (makeUserTablePTE etc.) |
| `kmazarin/kmazarin/abi_stubs_amd64.s:29-35` | HandlePageFaultAsm / HandleUserPageFaultAsm stubs |
| `kmazarin/ksyscall/mmap.go` | Userspace mmap allocator |
| `kmazarin/ksyscall/mmap_addr_amd64.go` | userMmapStart = 0x80000000 |

## Files Modified This Session

- `kmazarin/kmazarin/abi_stubs_amd64.s` — CS/SS from ThreadContext
- `kmazarin/kmazarin/save_context_amd64.go` — Save CS/SS
- `kmazarin/kmazarin/thread_context_amd64.go` — initThread0Context
- `kmazarin/kmazarin/thread_context_arm64.go` — initThread0Context (no-op)
- `kmazarin/kmazarin/thread_context_riscv64.go` — initThread0Context (no-op)
- `kmazarin/kmazarin/thread_context_stubs.go` — initThread0Context (no-op)
- `kmazarin/kmazarin/threads.go` — Call initThread0Context
- `kmazarin/kmazarin/exceptions_amd64.s` — CS validation, PF fix, breadcrumb cleanup
- `kmazarin/ksyscall/dispatch.go` — SysID-based table, arch_prctl entry
- `kmazarin/ksyscall/mmap.go` — userMmapStart extracted to arch files
- `kmazarin/ksyscall/mmap_addr_amd64.go` — NEW: userMmapStart = 0x80000000
- `kmazarin/ksyscall/mmap_addr_arm64.go` — NEW: userMmapStart = 0x10000000
- `kmazarin/ksyscall/mmap_addr_riscv64.go` — NEW: userMmapStart = 0x10000000
- `kmazarin/kmem/paging.go` — userMmapStart extracted to arch files
- `kmazarin/kmem/mmap_addr_amd64.go` — NEW: userMmapStart = 0x80000000
- `kmazarin/kmem/mmap_addr_arm64.go` — NEW: userMmapStart = 0x10000000
- `kmazarin/kmem/mmap_addr_riscv64.go` — NEW: userMmapStart = 0x10000000
- `cmd/gen-overlay/main.go` — Added fds_unix.go to ARM64/AMD64 overlays
- `diplomat/main/elf_loader.go` — Added main.syscallEntry to wantedSymbols

## Regression Testing Needed

ARM64 and RISC-V have NOT been tested with the dispatch table changes (SysID indexing). Run:
```bash
$GO tool task run TIMEOUT=15          # ARM64
$GO tool task run-riscv64 TIMEOUT=15  # RISC-V
```

Both should still boot, launch dapope.elf/stdio.elf, and show the clock + stdio window.
