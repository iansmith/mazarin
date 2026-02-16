# RISC-V Continuation Prompt — Session 6

## Current State (as of 2026-02-14)

RISC-V kmazarin boots fully: GPU framebuffer, VirtIO block, DTB parsing, timer
IRQs, context switches, demand paging, syscalls (including futex) all work.
Both dapope.elf and stdio.elf load and begin executing userspace code.

ARM64 is fully working — dapope renders clock, stdio renders text, no crashes.

**Two remaining RISC-V issues prevent full userspace operation:**

---

## Issue 1: Exception handler halts entire system on unrecoverable user fault

### Problem
When a userspace program crashes (e.g., NULL pointer dereference), the RISC-V
exception handler prints diagnostics and enters a WFI halt loop at
`pf_unhandled_halt` (exceptions_riscv64.s:726-728). This kills the entire
system, including other running programs like dapope.

ARM64 has the same problem (`el0_not_svc_hang` — infinite `B` loop at line
~2360 of exceptions_arm64.s). AMD64 just returns to the faulting instruction
(which retries infinitely). **None of the architectures properly kill the
faulting process.**

### Fix: Call ThreadExit from the exception handler

`ThreadExit()` in threads.go:1197 already does exactly what we need:
- Marks thread as `ThreadExited`
- Removes from all queues
- Releases TID and slot
- Decrements priest thread count, releases priest if last thread
- Finds next ready thread
- Returns `uintptr(unsafe.Pointer(&next.Context))` for context switch

**Implementation for RISC-V** (exceptions_riscv64.s):

At `pf_really_not_handled`, after printing diagnostics, instead of halting:

```asm
    // Check if fault came from user mode
    MOV  256(X2), T3          // saved sstatus
    AND  $0x100, T3, T3       // SPP bit
    BNE  T3, ZERO, pf_unhandled_halt  // Kernel fault → still halt

    // User fault: kill the process by calling ThreadExit
    GO_CALL_0_1(·ThreadExitAsm)
    // T0 = pointer to next ThreadContext (or 0 if no threads left)
    BEQ  T0, ZERO, pf_unhandled_halt  // No threads left → halt
    MOV  T0, S2               // S2 = ThreadContext pointer
    JMP  load_context_and_sret
```

You'll need:
1. **Add `ThreadExitAsm` declaration** in asm_decl.go (if not already there):
   ```go
   func ThreadExitAsm() uint64
   ```
2. **Add tail-call stub** in abi_stubs_riscv64.s:
   ```asm
   TEXT ·ThreadExitAsm(SB), NOSPLIT, $0-8
       JMP ·threadExitInternal(SB)
   ```
3. **Add `threadExitInternal`** in threads.go — a wrapper around `threadExitImpl`
   that returns `uint64` instead of `uintptr` (for ABI0 compatibility):
   ```go
   func threadExitInternal() uint64 {
       return uint64(threadExitImpl(&NormalSchedulerFunc))
   }
   ```

**Also fix ARM64**: Same pattern at `el0_not_svc_hang` — call ThreadExit instead
of infinite loop. This makes ARM64 more resilient too.

### Key files
- `kmazarin/kmazarin/exceptions_riscv64.s` — lines 726-728 (`pf_unhandled_halt`)
- `kmazarin/kmazarin/exceptions_arm64.s` — `el0_not_svc_hang` label
- `kmazarin/kmazarin/threads.go` — `ThreadExit()` at line 1197
- `kmazarin/kmazarin/abi_stubs_riscv64.s` — add ThreadExitAsm stub
- `kmazarin/kmazarin/asm_decl.go` — add declaration

### Testing
After this fix, when stdio crashes with NULL dereference, the kernel should:
1. Print the diagnostic (`PF@000000000004D430/?[...`)
2. Kill stdio's thread
3. Continue running dapope
4. Dapope should get CPU time and produce output

---

## Issue 2: stdio NULL dereference — NS16550 driver not registered

### Problem
stdio crashes at user VA 0x4D430 with a NULL pointer dereference immediately
after printing `[stdio] serial.Chars failed: serial: no serial device found`.

**Root cause**: The NS16550 UART driver is commented out in the device registry.

The call chain:
1. `kmazarin/device/registry.go:21` — `&uart.NS16550Driver{}` is commented out
2. `device.InitFromDTB()` scans DTB, finds `compatible = "ns16550a"` but no
   driver matches it
3. `device.GetByteStream()` returns nil → `SetupUartSoftIRQ()` never called →
   `uartIRQNum` stays 0
4. stdio calls `serial.Chars()` → `sys.QueryInputDevices()` → kernel returns
   empty serial device list → "no serial device found" error
5. stdio main() returns early (line 272 of flock/cmd/stdio/main.go)
6. Go runtime cleanup hits a nil pointer → crash at VA 0x4D430

ARM64 works because PL011 (`arm,pl011`) IS registered and matches DTB.

### Fix: Create and register NS16550 driver

The NS16550 driver doesn't exist yet — only `kmazarin/uart/pl011.go` exists.
There is no `kmazarin/uart/ns16550.go` file.

**Create `kmazarin/uart/ns16550.go`** modeled on pl011.go:

The NS16550 is simpler than PL011. Key registers:
- 0x00: RBR (read) / THR (write) — data register
- 0x01: IER — interrupt enable (bit 0 = receive data available)
- 0x02: IIR (read) / FCR (write) — interrupt ID / FIFO control
- 0x03: LCR — line control
- 0x05: LSR — line status (bit 0 = data ready, bit 5 = THR empty)

The driver needs to implement `deviceapi.Discoverable`:
- `Compatible() []string` → return `["ns16550a", "ns16550"]`
- `Probe(node *dtb.Node, base uintptr, irq int) deviceapi.Device`

And return a `deviceapi.ByteStream` implementation with:
- `ReadByte() (byte, bool)` — read from RBR if LSR bit 0 set
- `WriteByte(b byte)` — write to THR when LSR bit 5 set
- `EnableRXInterrupt()` — set IER bit 0
- `AckInterrupt()` — read IIR to clear

**RISC-V QEMU virt machine NS16550**: base PA 0x10000000, IRQ 10 (from DTB).
Diplomat's linear map puts it at VA 0xFFFFFFFF10000000. The early console
already uses this address (early_init_riscv64.go:26), confirming the UART works.

After creating the driver:
1. Uncomment `&uart.NS16550Driver{}` in registry.go:21
2. The DTB compatible match should work automatically
3. `GetByteStream()` returns the NS16550 instance
4. `SetupUartSoftIRQ()` gets called with the correct IRQ number
5. stdio's `serial.Chars()` succeeds

### But also fix the crash

Even with the NS16550 driver, the stdio NULL dereference is a bug — returning
from main() should not crash. The crash at VA 0x4D430 after `return` on line
272 suggests the Go runtime's exit path dereferences something nil. This might
be:
- The exit syscall handler not properly implemented for RISC-V
- Or a Go runtime function that assumes certain structures exist

Check what syscall `exit_group` (syscall 94) does on RISC-V. The user program's
Go runtime calls `exit_group` when main returns. If the kernel's handler for
this syscall is nil (returns -ENOSYS), the Go runtime might crash trying to
handle the unexpected error.

### Key files
- `kmazarin/uart/pl011.go` — reference implementation for NS16550 driver
- `kmazarin/device/registry.go:21` — uncomment NS16550 driver registration
- `kmazarin/device/deviceapi/` — interfaces the driver must implement
- `diplomat/main/dtb_riscv64.go` — DTB definition (ns16550a compatible, IRQ, base addr)
- `flock/cmd/stdio/main.go:269-273` — stdio serial.Chars error handling
- `kmazarin/kmazarin/early_init_riscv64.go:26` — confirms NS16550 VA 0xFFFFFFFF10000000

### Testing
```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Build all
$GO tool task

# Test RISC-V — expect dapope + stdio to both run without crashes
$GO tool task run-diplomat-riscv64 TIMEOUT=15
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log

# Test ARM64 — verify no regressions
$GO tool task run-diplomat-arm64 TIMEOUT=15
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

**Success criteria:**
- RISC-V: No system halt. dapope continues running after stdio crash (Issue 1).
- RISC-V: stdio finds serial device and initializes font rendering (Issue 2).
- ARM64: No regressions.

---

## Suggested Order

1. **Issue 1 first** (exception handler kill) — this is a quick fix and will
   immediately let dapope run even when stdio crashes. You can verify dapope
   works before tackling the NS16550 driver.

2. **Issue 2 second** (NS16550 driver) — more work (new file), but
   straightforward given pl011.go as reference.

## Important Build Note

When modifying kmazarin Go files, the Taskfile may think the RISC-V kmazarin
is "up to date" and skip rebuilding. Always force rebuild:
```bash
rm -f build/kmazarin-riscv64.elf build/diplomat-riscv64.elf build/disk-riscv64.img
```
Then run the task. Similarly for ARM64:
```bash
rm -f build/kmazarin.elf build/esp-arm64.img
```
