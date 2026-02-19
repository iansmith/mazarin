# AMD64 Continuation 3: stdio Window + Input Parity

## Current State (2026-02-18)
- **dapope.elf**: Launches, clock updates, timer/keyboard/mouse goroutines start.
  Crashes after ~10-20s on #GP in `runtime.gcResetMarkState.func1` due to allgs
  corruption (see below). Despite crash, proves demand paging, clone, SYSCALL,
  context switching, VirtIO GPU, and timer interrupts all work.
- **stdio.elf**: Launches, initializes framebuffer (1920x1080), loads font — but
  window never becomes visible / interactive before dapope crash kills the session.
- **VirtIO Input**: Keyboard and mouse PCI devices initialized, IOAPIC IRQs wired.
  dapope creates input goroutines but crash prevents testing actual input delivery.

## Outstanding Bug: allgs Corruption (#GP)
RIP=0x46ABE0 (`runtime.gcResetMarkState.func1`), CS=0x1B (Ring 3).
RAX=0x0000FFFFFFFF0002 (non-canonical → #GP).

allgs dump: `[0]C000058000 [1]0000FFFFFFFF0002 [2-7] valid g pointers`.
PA=0x77233000 (unified pool). Linear map confirms corruption is in physical page.
Pages are correctly zeroed at allocation (ZERO_VERIFY_FAIL didn't trigger).

### What was tried and ruled out:
- Page zeroing failure — pages verified zero after allocation
- PTE mismatch — linear map and user VA show same data
- CLONE_SETTLS missing — Go runtime uses SYSCALL with proper R8
- **Thread 0 running with wrong CR3** — Fixed by setting `t0.PageTableL0PA = GetKernelL0PA()`.
  Crash still occurs, so this is NOT the sole cause (but the fix should stay).
- **TLS sync stale write** — Fixed by skipping TLS sync when FSBase==0 in
  `load_context_and_iretq`. Thread 0's FSBase now captured from MSR during init.
  Crash still occurs.

### Most promising remaining theories:
1. **Buddy allocator double-allocation**: Same PA given to two different VAs.
   Add PA tracking in `HandleUserPageFault` — when PA is allocated, check if
   already in use. Use ring buffer of recent (PA, VA) pairs.
2. **Hardware write watchpoint**: Set DR0 to linear map VA of allgs page
   (0xFFFFFFFF77233000). #DB handler prints faulting RIP. This would directly
   identify the corrupting instruction.
3. **mmap VA overlap**: `userBumpAlloc` or MAP_FIXED could return overlapping
   ranges. Check mmap tracking spans for collisions.

### Diagnostic code in place (should be removed eventually):
- `diagnose_amd64.go`: walks page table for allgs backing array VA, reads via
  linear map and user VA, reports matches/mismatches.
- #GP handler in `exceptions_amd64.s` (~line 1000-1090): dumps allgs[0..7],
  calls DiagnosePageCorruption.
- Post-zeroing verification in `HandleUserPageFault` (paging.go ~line 722).

## Primary Task: Get stdio Window Visible and Interactive

### Step 1: Fix or work around allgs crash
The crash kills dapope after ~10-20s. Options:
- **Find and fix root cause** (see theories above)
- **Disable GC** in dapope (`debug.SetGCPercent(-1)`) as a workaround

### Step 2: Verify stdio window renders
stdio should already be rendering to the VirtIO GPU framebuffer. Check:
- Does stdio's `MapUserFramebuffer` succeed? (uses `gpu.GetFramebufferPA()`)
- Does stdio get scheduled (timer preemption working for both processes)?
- Does the window show up before the dapope crash?

### Step 3: Wire VirtIO Input to stdio
stdio needs keyboard/mouse events. On RISC-V, input is via PLIC → top-half →
bottom-half → NonTimerIRQTopHalf. On AMD64, check:
- IOAPIC routes for VirtIO keyboard (PCI 0:3.0) and mouse (PCI 0:4.0)
- `input_amd64.go` top-half handlers wired to exception dispatch
- Events flow from VirtIO queues to dapope/stdio input channels

### Key Files
- `kmazarin/kmazarin/exceptions_amd64.s` — exception/interrupt dispatch
- `kmazarin/device/virtio/input/input_amd64.go` — VirtIO input AMD64 glue
- `kmazarin/kmazarin/bottom_half.go` — soft IRQ bottom-half processing
- `kmazarin/kmazarin/diagnose_amd64.go` — allgs diagnostic (temporary)
- `kmazarin/kmem/paging.go` — demand paging, HandleUserPageFault
- `kmazarin/ksyscall/mmap.go` — userspace mmap handler

### Build & Run
```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
$GO tool task run-x86_64 TIMEOUT=30
$GO tool safe-serial-read /tmp/diplomat-serial.log
```
