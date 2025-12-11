# Debugging SP Misalignment in RenderChar16x16

This guide explains how to use GDB to debug the stack pointer (SP) misalignment issue that causes crashes in `RenderChar16x16`.

## Problem Summary

- SP is aligned (16-byte boundary) at entry to `RenderChar16x16` (0x40237300)
- SP becomes misaligned during execution (crash FAR: 0x40237342/0x40237343)
- Crash occurs after bitmap access but before the rendering loop

## Quick Start

### Option 1: Simple Step Tracing (Recommended)

1. **Start QEMU with GDB server** (Terminal 1):
   ```bash
   cd /Users/iansmith/mazzy
   source enable-mazzy
   docker/mazboot -g
   ```

2. **Connect GDB and load script** (Terminal 2):
   ```bash
   cd /Users/iansmith/mazzy/src
   source ../enable-mazzy
   ./gdb-connect.sh
   ```
   
   In GDB:
   ```gdb
   (gdb) source debug-sp-simple.gdb
   (gdb) continue
   ```

3. **When breakpoint hits at RenderChar16x16**:
   ```gdb
   (gdb) step-trace-sp
   ```
   This will step through instructions checking SP alignment at each step.

### Option 2: Comprehensive Breakpoints

1. **Start QEMU with GDB server** (Terminal 1):
   ```bash
   cd /Users/iansmith/mazzy
   source enable-mazzy
   docker/mazboot -g
   ```

2. **Connect GDB and load script** (Terminal 2):
   ```bash
   cd /Users/iansmith/mazzy/src
   source ../enable-mazzy
   ./gdb-connect.sh
   ```
   
   In GDB:
   ```gdb
   (gdb) source debug-sp-watchpoint.gdb
   (gdb) continue
   ```

   This sets breakpoints at all functions in the call chain and checks SP alignment at each.

## GDB Scripts

### `debug-sp-simple.gdb`
- Sets breakpoint at `RenderChar16x16` entry
- Provides `step-trace-sp` command to step through checking SP
- Simpler, more interactive approach

### `debug-sp-watchpoint.gdb`
- Sets breakpoints at all functions in the call chain:
  - `FramebufferPuts`
  - `FramebufferPutc`
  - `FramebufferPutc16x16`
  - `RenderCharAtCursor16x16`
  - `RenderChar16x16`
- Checks SP alignment at each breakpoint
- Automatically stops if SP becomes misaligned

## Useful GDB Commands

### Check SP Alignment
```gdb
(gdb) print/x $sp
(gdb) print/x $sp & 0xF    # Low 4 bits (should be 0 for 16-byte alignment)
```

### Step Through Code
```gdb
(gdb) stepi                 # Step one instruction
(gdb) nexti                 # Step one instruction (skip function calls)
(gdb) step                  # Step one source line
(gdb) next                  # Step one source line (skip function calls)
```

### Inspect Code
```gdb
(gdb) x/20i $pc-0x30        # Disassemble 20 instructions around PC
(gdb) x/10x $sp             # Show 10 words at stack pointer
(gdb) backtrace             # Show call stack
(gdb) info registers         # Show all registers
```

### Find Function Addresses
```gdb
(gdb) info address main.RenderChar16x16
(gdb) print &main.RenderChar16x16
```

## Function Address Ranges

From `objdump -t kernel-qemu.elf`:
- `main.RenderChar16x16`: 0x2be5e0 - 0x2be860
- `main.RenderCharAtCursor16x16`: 0x2be860 - 0x2be8c0
- `main.FramebufferPutc16x16`: 0x2bed00 - 0x2bedc0
- `main.FramebufferPutc`: 0x2bedc0 - 0x2bee00
- `main.FramebufferPuts`: 0x2bee00 - 0x2beeb0

## What to Look For

1. **SP alignment at function entry**: Should be 0x...0, 0x...8 (16-byte aligned)
2. **SP changes**: Watch for instructions that modify SP incorrectly
3. **Stack accesses**: Look for `str`/`ldr` instructions with misaligned offsets
4. **Function calls**: Check if called functions preserve SP alignment

## Expected Behavior

Based on breadcrumbs:
- `[A]` FramebufferPuts: SP=0x402373C0 (aligned)
- `[B]` FramebufferPutc: SP=0x402373A0 (aligned)
- `[C]` FramebufferPutc16x16: SP=0x40237380 (aligned)
- `[D]` RenderCharAtCursor16x16: SP=0x40237350 (aligned)
- `[E]` RenderChar16x16 entry: SP=0x40237300 (aligned)
- `[G]` After bitmap access: SP=0x40237300 (still aligned)
- **Crash**: FAR=0x40237342 (misaligned!)

The corruption happens between `[G]` and the crash, likely during:
- Loop initialization
- Variable assignments
- Function prologue of a called function

## Troubleshooting

### GDB can't connect
- Make sure QEMU is running: `lsof -i :1234`
- Check QEMU output for GDB server messages

### Breakpoints not working
- Verify kernel has debug symbols: `file kernel-qemu.elf`
- Check function addresses: `objdump -t kernel-qemu.elf | grep RenderChar`

### SP watchpoint too slow
- Use `debug-sp-simple.gdb` with manual stepping instead
- Set breakpoints at specific addresses rather than watching SP continuously
