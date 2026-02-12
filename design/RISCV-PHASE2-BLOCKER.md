# RISC-V Phase 2 Blocker - DiplomatEntry Function Prologue Failure

## Current State

**Output:** `D1234567TCJEW`

**What works:**
- ✅ Trampoline executes (D1234567TC)
- ✅ g0/m0/TLS initialized correctly
- ✅ Jump to diplomatEntryWrapper (J)
- ✅ Wrapper entry ('E')
- ✅ Before CALL to DiplomatEntry ('W')
- ✅ CALL instruction executes (no 'F' = no fall-through)

**What fails:**
- ❌ DiplomatEntry function body NEVER executes
- ❌ Even first instruction (`uartWriteDirect('G')`) doesn't run
- ❌ printString never called

## Root Cause

**DiplomatEntry's compiler-generated function prologue is crashing.**

DiplomatEntry is a **Go function** (not pure assembly), so the Go compiler generates a prologue that:
1. Checks stack guards
2. May allocate stack frame
3. Sets up for potential stack split

This prologue is failing before any user code executes.

## Investigation Steps Taken

### 1. Calling Convention Tests
- ❌ `JMP ·DiplomatEntry(SB)` - fails
- ❌ `JALR ZERO, (T0)` - fails
- ❌ `CALL ·DiplomatEntry(SB)` - fails

### 2. ABI Wrapper Tests
- Tried calling `.abi0` wrapper explicitly - still fails
- Symbol table shows both `main.DiplomatEntry` and `main.DiplomatEntry.abi0` exist

### 3. Minimal Test Cases
- Created `uartWriteDirect('G')` as first line of DiplomatEntry - never executes
- Removed all debug calls - still crashes
- Even empty function body crashes

### 4. Runtime Comparison
- ARM64 uses `BL main·DiplomatEntry(SB)` - works fine
- ARM64 sets up stack guards before calling - we do the same
- RISC-V runtime uses `JMP main(SB)` then `JALR ZERO, T0` - we tried both

## Key Observations

1. **Wrapper code executes fine** - we can write to UART from assembly wrapper
2. **Jump/Call succeeds** - we don't see 'F' marker (post-CALL code)
3. **Go function prologue fails** - before ANY user code runs
4. **Stack is set up** - trampoline sets SP=0x81218000, guards set correctly

## Files Involved

### Created/Modified for Phase 2:
- `diplomat/main/uart_riscv64.s` - Direct UART driver
- `diplomat/main/uart_riscv64.go` - UART Go wrappers
- `diplomat/main/uart_direct_riscv64.s` - Minimal UART for testing
- `diplomat/main/diplomat_entry_wrapper_riscv64.s` - Assembly wrapper
- `diplomat/main/platform_riscv64.go` - printCharRISCV for non-UEFI
- `diplomat/main/main.go` - printString fix, test code
- `diplomat/main/tls_riscv64.s` - debugPortOut UART impl
- `runtime-patches/diplomat-linux/trampoline_riscv64.s` - Debug markers

## Hypothesis

The Go function prologue may be:
1. **Accessing invalid memory** for stack guard check
2. **Stack misalignment** - RISC-V requires 16-byte alignment
3. **Missing runtime state** that ARM64 UEFI provides but we don't
4. **TLS access failing** in the prologue (g register access)

## Next Steps to Try

### Option 1: Pure Assembly DiplomatEntry
Replace DiplomatEntry with pure assembly (like ARM64's `_efi_main_arm64`):
```asm
TEXT ·DiplomatEntryAsm(SB), NOSPLIT|NOFRAME, $0
    // Write 'G' to prove entry
    // Then call Go helper functions
    CALL ·diplomatEntryGo(SB)
    RET
```

### Option 2: Check Stack Alignment
Verify SP is 16-byte aligned when calling DiplomatEntry:
```asm
AND $15, SP, X6     // Check lower 4 bits
// If non-zero, stack misaligned
```

### Option 3: Disassemble DiplomatEntry Prologue
Use objdump to see exact prologue instructions:
```bash
bin/target-objdump -d build/diplomat-riscv64.elf | grep -A20 "DiplomatEntry>:"
```

### Option 4: Compare with Working Runtime
Look at how `runtime·rt0_go` is called and replicate that exact pattern.

### Option 5: Use Debugger
Connect GDB to QEMU and single-step through DiplomatEntry prologue to see exactly where it crashes.

## Success Criteria

Once DiplomatEntry executes:
- Should see 'G' (from uartWriteDirect)
- Should see "Diplomat UEFI Bootloader" message
- printString will work
- Phase 2 complete!

## Memory Layout (from trampoline)

```
Code base:    0x80000000
Stack:        0x81218000 (32KB, grows down)
Stack guard:  SP - 64KB
Page tables:  0x81200000+ (for future MMU)
```

## Register State at DiplomatEntry Call

```
SP (X2):      0x81218000 (set by trampoline)
g (X27):      Points to runtime.g0
TP (X4):      Points to TLS (tlsBlock + 8)
RA (X1):      Return address from CALL
Other:        Standard RISC-V calling convention
```

## Build Commands

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU_RISCV64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-riscv64

# Build
rm -f build/diplomat-riscv64.elf
$GO tool task diplomat-riscv64

# Run
$GO tool task run-diplomat-riscv64 TIMEOUT=5

# Check output
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

## References

- ARM64 working example: `diplomat/main/entry_arm64.s:81` (uses `BL`)
- RISC-V runtime entry: `/opt/homebrew/Cellar/go/1.25.5/libexec/src/runtime/rt0_linux_riscv64.s`
- Go calling convention: `JMP` and `JALR ZERO` used in runtime

---

**Status:** Blocked on DiplomatEntry prologue failure
**Next Session:** Try Option 1 (pure assembly entry) or Option 3 (disassemble prologue)
