# Debugging Strategy for 0xDEAD000E Page Fault

## Summary of Findings

**CRITICAL DISCOVERY:** `0xDEAD000E` is NOT a coincidence - it's the test value for register X14!

From `exceptions.s:260-261`:
```asm
movz x14, #0xDEAD, lsl #16    // X14 upper 16 bits = 0xDEAD
movk x14, #0x000E              // X14 lower 16 bits = 0x000E
                               // Result: X14 = 0xDEAD000E
```

This is part of `test_print_functions_preserve_registers` which sets each register to `0xDEAD0000 + register_number` to verify register preservation.

**The Problem:** Kmazarin is **dereferencing X14 as a pointer**, treating this test pattern as a real memory address. This indicates register corruption or misuse.

---

## Key Questions to Answer

### Question 1: Where does X14 get set to 0xDEAD000E?
- **Method:** Set breakpoint at `exceptions.s` line 260-261 (test function)
- **Check:** Is this test even running? Or is X14 being set elsewhere?
- **GDB Command:**
  ```gdb
  break test_print_functions_preserve_registers
  watch $x14
  ```

### Question 2: What code dereferences X14?
- **Method:** Catch the page fault and examine backtrace
- **Check:** Which kmazarin function is accessing memory via X14?
- **GDB Commands:**
  ```gdb
  break HandlePageFault
  # When fault addr == 0xDEAD000E:
  backtrace
  disassemble
  info registers
  ```

### Question 3: Why does kmazarin have X14 = 0xDEAD000E?
- **Hypothesis A:** Exception handler corrupted X14 during syscall
- **Hypothesis B:** Go runtime scheduler/goroutine switch corrupted X14
- **Hypothesis C:** Stack corruption overwrote saved X14 value
- **Method:** Trace X14 value through exception/syscall path

### Question 4: Which register should contain the real pointer?
- **Method:** Examine the faulting instruction in kmazarin
- **Check:** What register was SUPPOSED to be used? What's its actual value?
- **GDB Commands:**
  ```gdb
  # At fault:
  disassemble $pc-20,$pc+20
  info registers
  # Compare X14 with other registers - which one has a valid address?
  ```

### Question 5: Is this the first time X14 is corrupted?
- **Method:** Set watchpoint on X14 early in boot
- **Check:** When does X14 first become 0xDEAD000E?
- **GDB Command:**
  ```gdb
  watch $x14
  # Track every X14 modification
  ```

---

## Debugging Phase 1: Catch and Analyze

### Step 1: Run with basic script
```bash
# Terminal 1: Start QEMU with GDB
make qemu-gdb

# Terminal 2: Run GDB with script
gdb -x debug_dead000e.gdb
# Type 'continue' at the (gdb) prompt
```

### Step 2: When breakpoint hits
At the HandlePageFault breakpoint:
```gdb
# Verify it's our fault
print/x $x0
# Should show 0xdead000e or 0x0dead000e

# Get full context
info registers
backtrace 30

# Examine the faulting instruction
# ELR_EL1 contains the faulting PC
print/x $elr_el1
disassemble $elr_el1-20, $elr_el1+20

# Look for which register is being dereferenced
# Common patterns:
#   ldr x0, [x14]          <- loading from x14 as pointer
#   str x1, [x14, #8]      <- storing to x14+8
#   ldr x2, [x14, x3]      <- loading from x14+x3
```

### Step 3: Find the actual bug
```gdb
# Check exception frame to see saved X14
x/40gx $sp    # Exception frame is on SP_EL1

# If X14 is saved at offset 112 (14*8):
print/x *((uint64_t*)($sp + 112))

# Trace backwards: examine previous exception frames
# Look for when X14 changed from valid value to 0xDEAD000E
```

---

## Debugging Phase 2: Root Cause Analysis

### Hypothesis A: Exception Handler Bug

**Test:** Check if X14 is properly saved/restored during syscalls

```gdb
# Break on syscall entry
break handle_svc_syscall

# Check X14 value before handler
commands
  silent
  printf "Syscall entry: X14=0x%lx\n", $x14
  continue
end

# Break on syscall return
break syscall_return

# Check X14 value after handler
commands
  silent
  printf "Syscall return: X14=0x%lx\n", $x14
  continue
end
```

**Expected:** X14 should be preserved across syscalls (callee-saved register)

**Check in exceptions.s:**
- Line 1247-1253: Is X14 saved before calling Go?
- Line 1632+: Is X14 restored before eret?

### Hypothesis B: Go Runtime Scheduler Bug

**Test:** Check if goroutine context switch corrupts X14

```gdb
# Break on context switch functions
break runtime.gogo
break runtime.mcall

# Examine saved goroutine context (g.sched)
# X14 should be preserved in g.sched registers
```

### Hypothesis C: Test Code Still Active

**Test:** Check if test_print_functions_preserve_registers is actually running

```gdb
# Break at test entry
break test_print_functions_preserve_registers

# This test should NOT run during kmazarin initialization!
# If it does, we have a control flow bug
```

---

## Debugging Phase 3: Find Who Sets X14

### Watchpoint Strategy

```gdb
# Set hardware watchpoint on X14
watch $x14

# This will stop on EVERY write to X14
# Look for the write that sets it to 0xDEAD000E

commands
  silent
  if $x14 == 0xdead000e
    printf "*** X14 set to DEAD000E! ***\n"
    backtrace
    disassemble $pc-20,$pc+20
    # Stop for investigation
  else
    # Continue for other X14 writes
    continue
  end
end
```

---

## Expected Outcomes

### Scenario 1: Exception Handler Bug
**Symptom:** X14 = 0xDEAD000E after syscall return
**Root Cause:** Exception handler not preserving X14 correctly
**Fix:** Update exceptions.s to properly save/restore X14

### Scenario 2: Uninitialized Register
**Symptom:** X14 = 0xDEAD000E at kmazarin entry
**Root Cause:** Kmazarin started with uninitialized X14
**Fix:** Initialize all registers before jumping to kmazarin

### Scenario 3: Stack Corruption
**Symptom:** Saved X14 on stack is corrupted
**Root Cause:** Stack overflow or buffer overrun
**Fix:** Increase stack size or fix buffer overflow

### Scenario 4: Go Compiler Bug
**Symptom:** Kmazarin code uses X14 as pointer when it shouldn't
**Root Cause:** Incorrect register allocation by Go compiler
**Fix:** Investigate Go compiler flags or runtime initialization

---

## Quick Commands Reference

```bash
# Start debugging session
make qemu-gdb &
gdb -x debug_dead000e.gdb

# At GDB prompt:
continue                          # Run until breakpoint
info registers                    # Show all registers
print/x $x14                      # Show X14 in hex
backtrace 30                      # Show call stack
disassemble                       # Show current code
x/20gx $sp                        # Show stack contents
watch $x14                        # Watch X14 changes
info breakpoints                  # List breakpoints
info watchpoints                  # List watchpoints

# After finding the bug:
quit
make run                          # Test the fix
```

---

## Notes

- X14 is a **callee-saved register** in ARM64 AAPCS64 - must be preserved across function calls
- The test pattern `0xDEAD0000 + N` is used to detect register corruption
- Register X14 maps to exception frame offset 112 (14 * 8 bytes)
- Check if kmazarin is compiled with `-buildmode=exe` or custom flags that affect register usage
