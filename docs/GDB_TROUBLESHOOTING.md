# GDB Debugging - Troubleshooting Guide

## Problem: GDB + QEMU Hung with Zero Output

### Root Cause

The original `debug_catch_svc.gdb` script used a **watchpoint on UART MMIO address** (`0x09000000`):

```gdb
watch *($uart_base)
```

**Why this caused hanging:**
1. UART at `0x09000000` is **memory-mapped I/O**, not normal RAM
2. Watchpoints on MMIO regions in QEMU can cause:
   - Extreme slowdown (100-1000x slower)
   - Complete hang (QEMU waiting for GDB, GDB waiting for QEMU)
   - Zero output because boot never progresses
3. QEMU's GDB stub doesn't handle MMIO watchpoints well

### Solution: Use Function Breakpoints Only

Three new scripts have been created, in order of complexity:

---

## Option 1: Ultra-Simple UART Trace (Recommended First)

**File:** `debug_uart_trace.gdb`

**What it does:**
- Prints every character written to UART
- Auto-continues (doesn't stop)
- You can watch for "SVC=" in the output stream

**Usage:**
```bash
# Terminal 1: Start QEMU with GDB server
NOGRAPHIC=1 DEBUG=1 bin/run-mazboot

# Terminal 2: Start GDB
~/mazzy/bin/target-gdb -x debug_uart_trace.gdb flash/mazboot.elf

# In GDB:
(gdb) target remote :1234
(gdb) c

# Watch the output - you'll see all UART characters printed
# Look for "SVC=" appearing in the stream
# Press Ctrl+C to stop
```

**Expected output:**
```
Connecting to QEMU...
0x40100000 in ?? ()
(gdb) c
Continuing.
00000000DEAD0000PRINT_TEST: PASS
.text: 0x0000000040100000...
[output continues...]
Jumping to kmazarin...
MSVC=00000000 fatal error...
^C
(gdb) bt
[shows where it was when you pressed Ctrl+C]
```

---

## Option 2: Pattern Detection (Stops on Match)

**File:** `debug_catch_svc_simple.gdb`

**What it does:**
- Tracks last 4 characters written
- Detects "S", "V", "C", "=" sequence
- **STOPS** when pattern detected
- Shows backtrace of where it came from

**Usage:**
```bash
# Terminal 1: Start QEMU
NOGRAPHIC=1 DEBUG=1 bin/run-mazboot

# Terminal 2: Start GDB
~/mazzy/bin/target-gdb -x debug_catch_svc_simple.gdb flash/mazboot.elf

# In GDB:
(gdb) target remote :1234
(gdb) c

# GDB will auto-continue until it sees "SVC=" pattern
# Then it stops and shows you the backtrace
```

**When it detects "SVC=":**
```
MSVC=
*** DETECTED SVC= PATTERN ***
Last 4 chars: 'S' 'V' 'C' '='

Backtrace:
#0  0x... in main.uartPutcDirect ()
#1  0x... in some_function ()
#2  0x... in caller ()
...

Stopping for investigation. Type 'c' to continue.
```

---

## Option 3: Manual Stepping (Full Control)

**What to do:**
```bash
# Terminal 1: Start QEMU
NOGRAPHIC=1 DEBUG=1 bin/run-mazboot

# Terminal 2: Start GDB with NO script
~/mazzy/bin/target-gdb flash/mazboot.elf

# In GDB:
(gdb) target remote :1234
(gdb) break main.uartPutcDirect
(gdb) commands
> silent
> printf "%c", (char)$x0
> continue
> end
(gdb) c

# Now manually watch output, press Ctrl+C when you see "SVC="
# Then examine backtrace
```

---

## Understanding GDB + QEMU Interaction

### How DEBUG=1 Works

When you run `DEBUG=1 bin/run-mazboot`, QEMU starts with:
```
-s -S
```

- `-s` = GDB server on port 1234
- `-S` = Freeze at startup, wait for GDB to connect

**This is why you see zero output** - QEMU is waiting for GDB!

### Normal Flow

1. QEMU starts, **FREEZES** at first instruction
2. GDB connects: `target remote :1234`
3. GDB sends "continue" command: `c`
4. QEMU starts executing
5. When breakpoint hits, QEMU freezes again
6. GDB shows you where it stopped
7. You type `c` again to continue

### Why Original Script Hung

The watchpoint created an infinite loop:
```
1. QEMU writes to UART → watchpoint triggers
2. GDB stops QEMU
3. GDB script runs "continue"
4. QEMU writes to UART → watchpoint triggers again
5. [infinite loop, extremely slow]
```

With MMIO watchpoints, step 1-2 is **extremely slow** (milliseconds per character instead of microseconds), making boot take hours instead of seconds.

---

## Alternative: Just Run Without GDB

If GDB proves too slow, you can just run normally and grep for context:

```bash
NOGRAPHIC=1 timeout 10 bin/run-mazboot 2>&1 | grep -B 5 -A 5 "SVC="
```

This shows 5 lines before and after "SVC=" appears, which might give clues.

---

## Quick Reference

| Script | Speed | Detail | Use When |
|--------|-------|--------|----------|
| `debug_uart_trace.gdb` | Fast | Low | Want to see all output, find SVC= manually |
| `debug_catch_svc_simple.gdb` | Medium | High | Want automatic detection + backtrace |
| No script (manual) | Fastest | Custom | Want full control |
| No GDB (grep) | Instant | None | Just want to see context around SVC= |

---

## Tips

1. **Start with debug_uart_trace.gdb** - it's fast and shows everything
2. **Use Ctrl+C** to interrupt anytime and examine state
3. **Check backtrace** after Ctrl+C: `(gdb) bt`
4. **Print variables**: `(gdb) print $x0`
5. **If it's too slow**, just use grep without GDB

---

## Files

- ✅ `debug_uart_trace.gdb` - Simple trace, recommended
- ✅ `debug_catch_svc_simple.gdb` - Pattern detection
- ❌ `debug_catch_svc.gdb` - **DON'T USE** - has MMIO watchpoint bug
