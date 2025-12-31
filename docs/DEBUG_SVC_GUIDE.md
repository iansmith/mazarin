# Guide to Catching the Mysterious "SVC=" Output

## Problem
The output "SVC=00000000" appears after "Jumping to kmazarin..." but we cannot find it in the source code.

## Solution: GDB Debugging Script

A GDB script (`debug_catch_svc.gdb`) has been created to catch whoever is writing "SVC=" to UART.

### How to Use

1. **Start QEMU in debug mode** (in one terminal):
   ```bash
   NOGRAPHIC=1 DEBUG=1 bin/run-mazboot
   ```
   This will start QEMU with `-s -S` (GDB server on port 1234, wait for debugger)

2. **Start GDB with the script** (in another terminal):
   ```bash
   cd ~/mazzy
   ~/mazzy/bin/target-gdb -x debug_catch_svc.gdb flash/mazboot.elf
   ```

3. **Connect to QEMU**:
   ```
   (gdb) target remote :1234
   ```

4. **Start execution**:
   ```
   (gdb) continue
   ```

5. **Wait for detection**:
   When "SVC=" is written, GDB will:
   - Stop execution automatically
   - Show the backtrace (where the write came from)
   - Show all registers
   - Show the current code location

### What the Script Does

The script sets up three detection mechanisms:

1. **UART Watchpoint**: Watches memory address `0x09000000` (UART data register) for any writes. Tracks the last 4 characters written and stops when it sees the sequence 'S', 'V', 'C', '='.

2. **uartPutcDirect Breakpoint**: Catches calls to the Go function `main.uartPutcDirect` and checks if the character is part of the "SVC=" sequence.

3. **uartPutsDirect Breakpoint**: Catches calls to the Go function `main.uartPutsDirect` and checks if the string contains "SVC=".

### Example Output When Caught

```
*** CAUGHT SVC= PATTERN! ***
Character sequence: S(0x53) V(0x56) C(0x43) =(0x3D)

Backtrace:
#0  0x... in some_function ()
#1  0x... in caller_function ()
...

Registers:
x0             0x...
x1             0x...
...

Current location:
123    some line of code
124    another line
...

Execution stopped. Type 'continue' to resume.
```

## Code Changes Made

To make detection easier, the following changes were made:

1. **Disabled all 'S' breadcrumbs**:
   - Disabled "SYS:" output in `HandleSyscall` (exceptions.go)
   - Disabled "SVC from EL1/EL0" messages in `ExceptionHandler` (exceptions.go)

2. **Created safe UART macros** (exceptions.s):
   - `DEBUG_CALL_GO_PUTC` - safely call `uartPutcDirect` while preserving X0
   - `DEBUG_CALL_GO_PUTS` - safely call `uartPutsDirect` while preserving X0
   - `DEBUG_CALL_GO_PUTHEX64` - safely call `uartPutHex64Direct` while preserving X0
   - `DEBUG_CALL_GO_PUTHEX32` - safely call `uartPutHex32Direct` while preserving X0

   All macros save/restore ALL registers (especially X0) around Go function calls.

## Alternative: Run Without GDB

If you want to see the raw output without GDB:

```bash
NOGRAPHIC=1 bin/run-mazboot 2>&1 | grep -B 5 -A 5 "SVC="
```

This will show 5 lines before and after "SVC=" appears, which might give clues about context.

## Notes

- The mysterious output might be coming from QEMU itself, not our code
- It might be kmazarin's runtime (though we checked and found nothing)
- It might be memory corruption causing random characters
- The GDB script should definitively tell us where it's coming from
