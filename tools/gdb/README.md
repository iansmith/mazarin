# GDB Debugging Scripts for Mazzy Kernel

This directory contains GDB scripts for debugging the Mazzy bare-metal kernel running in QEMU.

## Quick Start

### Watch SP Changes (Recommended for Current Bug)

This script tracks SP register changes instruction-by-instruction to identify when/how SP gets corrupted:

```bash
# Terminal 1: Start QEMU with GDB server
cd /Users/iansmith/mazzy
source enable-mazzy
mazboot -g

# Terminal 2: Run GDB with SP watching script
cd /Users/iansmith/mazzy
source enable-mazzy
cd src
target-gdb kernel-qemu.elf -x ../tools/gdb/watch-sp-changes.gdb
(gdb) continue
```

The script will:
- Track SP at function entry
- Save SP after breadcrumb [b]
- Check SP at each instruction
- **Stop at 0x2be870** (right before crash) for manual inspection
- Show SP changes, target address alignment, and register state

### Manual Debugging Session

For interactive debugging:

```bash
# Terminal 1: Start QEMU
mazboot -g

# Terminal 2: Connect GDB
target-gdb kernel-qemu.elf -x ../tools/gdb/debug-crash-working.gdb
(gdb) continue
```

## Available Scripts

### Active Scripts (Use These)

- **`watch-sp-changes.gdb`** - ⭐ **NEW**: Tracks SP changes instruction-by-instruction
  - Sets breakpoints at every instruction between [b] and crash
  - Compares SP at each step to detect changes
  - Stops before crash for inspection
  - Shows alignment status and target address

- **`debug-crash-working.gdb`** - Manual debugging at crash location
  - Sets breakpoint at 0x2be868 (crash location)
  - Shows SP, alignment, disassembly, registers
  - Use with `source` command in manual GDB session

- **`debug-crash-location.gdb`** - Similar to above with more context
  - Multiple breakpoints in RenderChar16x16
  - Context around crash

### Historical Scripts (Reference Only)

These were earlier attempts with various approaches:

- `debug-crash-batch.gdb` - Batch mode attempt (didn't work reliably)
- `debug-simple.gdb` - Simplified batch script
- `debug-sp-batch.gdb` - Batch mode with SP checks
- `debug-sp-simple.gdb` - Step-by-step tracing
- `debug-sp-step.gdb` - Automated stepping
- `debug-sp-watchpoint.gdb` - Watchpoint-based (hardware watchpoints don't work well on registers)
- `debug-sp-watchpoint-hw.gdb` - Hardware watchpoint variant
- `debug.gdb` - Basic debugging setup

## Helper Scripts

- **`debug-crash.sh`** - Automates launching QEMU and connecting GDB
  ```bash
  cd /Users/iansmith/mazzy
  source enable-mazzy
  tools/gdb/debug-crash.sh
  ```

## Current Bug Investigation

### The Problem

SP mysteriously changes between breadcrumb `[b]` and crash:
- **Breadcrumb [b] SP**: 0x402372D0 (aligned)
- **Crash FAR**: 0x40237322 (misaligned)
- **Calculated SP at crash**: 0x40237300
- **SP difference**: 0x30 (48 bytes)

**No instructions between [b] and crash modify SP!**

### What We're Looking For

Using `watch-sp-changes.gdb`, we want to find:

1. **When does SP change?**
   - Between which two instructions?
   - Or does it not change and something else is wrong?

2. **How does SP change?**
   - Direct modification?
   - Interrupt corruption?
   - Compiler optimization issue?

3. **What's the real SP value?**
   - Is `get_stack_pointer()` returning the correct value?
   - Is SP actually changing or is our measurement wrong?

### Expected Output

When running `watch-sp-changes.gdb`, you should see:

```
=== RenderChar16x16 Entry ===
SP = 0x...

=== After Breadcrumb [b] ===
SP = 0x402372D0
Saved SP for comparison: 0x402372D0

(checks SP at each instruction - if it changes, prints "!!! SP CHANGED !!!")

========================================
=== RIGHT BEFORE CRASH INSTRUCTION ===
========================================
Current SP: 0x... (this is the critical value!)
Saved SP:   0x402372D0

(script stops here - don't continue if SP is wrong)
```

### Next Steps After Running

1. **If SP changes between instructions**:
   - Note which instruction caused the change
   - Examine that instruction's assembly
   - Check if an interrupt fired (look for exception markers)

2. **If SP doesn't change**:
   - But crash still happens → issue is with how we measure SP
   - Check `get_stack_pointer()` implementation
   - Examine compiler's stack layout assumptions

3. **If SP is already wrong at [b]**:
   - Problem happens earlier in function
   - Check function prologue
   - Check previous function calls

## GDB Commands Reference

### Useful Commands During Debugging

```gdb
# Check current SP
print $sp
print/x $sp

# Check SP alignment
print $sp & 0xF

# Calculate target address
print/x $sp + 34

# Examine memory at stack
x/10gx $sp

# Show all registers
info registers

# Show specific registers
info registers x0 x1 x2 sp x28 x29 x30

# Disassemble around current location
disassemble

# Show source (if available)
list

# Set new breakpoint
break *0xADDRESS

# Delete breakpoint
delete 1

# Continue execution
continue

# Single step (instruction level)
stepi

# Step over function calls
nexti
```

### Watching for Changes

```gdb
# Watch a memory location (not registers)
watch *0x40237300

# Watch when a variable changes
watch myVariable

# Set conditional breakpoint
break *0x2be870 if $sp != 0x402372D0
```

## Tips

1. **Use `source` for scripts**: Instead of `-x` flag, you can source scripts interactively:
   ```gdb
   (gdb) source ../tools/gdb/watch-sp-changes.gdb
   ```

2. **Interrupt execution**: Press Ctrl+C in GDB to pause execution

3. **Save output**: Redirect GDB output to file:
   ```bash
   target-gdb kernel-qemu.elf -x ../tools/gdb/watch-sp-changes.gdb 2>&1 | tee gdb-output.log
   ```

4. **Multiple terminals**: Keep QEMU output visible in terminal 1 while running GDB in terminal 2

5. **Reset cleanly**: If QEMU hangs, kill it and restart:
   ```bash
   pkill -f qemu-system-aarch64
   mazboot -g
   ```

## Documentation

See also:
- `/Users/iansmith/mazzy/docs/GDB-DEBUGGING-REVIEW.md` - Review of debugging attempts
- `/Users/iansmith/mazzy/docs/GDB-MANUAL-INSTRUCTIONS.md` - Manual debugging guide
- `/Users/iansmith/mazzy/docs/NOT-COMPILER-BUG-NEW-ANALYSIS.md` - Current analysis
- `/Users/iansmith/mazzy/docs/RUN-GDB-DEBUG.md` - Quick start guide

