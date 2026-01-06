# Analysis: Syscall Interface - How Go Makes System Calls

## Summary

This test reveals the **actual syscall interface** - how Go transitions from user code to kernel via the SVC (Supervisor Call) trap instruction.

## The Syscall Stack

```
Application Code (os.Create, os.Write, etc.)
    ↓
os package (os.OpenFile, os.File.Write, etc.)
    ↓
syscall package (syscall.Open, syscall.Write, etc.)
    ↓
syscall.Syscall6 (Go wrapper with runtime hooks)
    ↓
syscall.RawSyscall6 (minimal wrapper)
    ↓
internal/runtime/syscall.Syscall6.abi0 (THE ACTUAL SVC!)
```

## The Critical Function: Syscall6.abi0

Located at `0x12d30`, this is where the **actual transition to kernel** happens.

### Complete Disassembly

```assembly
0000000000012d30 <internal/runtime/syscall.Syscall6.abi0>:
   12d30:  f94007e8  ldr   x8, [sp, #8]        # syscall number → X8
   12d34:  f9400be0  ldr   x0, [sp, #16]       # arg1 → X0
   12d38:  f9400fe1  ldr   x1, [sp, #24]       # arg2 → X1
   12d3c:  f94013e2  ldr   x2, [sp, #32]       # arg3 → X2
   12d40:  f94017e3  ldr   x3, [sp, #40]       # arg4 → X3
   12d44:  f9401be4  ldr   x4, [sp, #48]       # arg5 → X4
   12d48:  f9401fe5  ldr   x5, [sp, #56]       # arg6 → X5
   12d4c:  d4000001  svc   #0x0                # *** TRAP TO KERNEL ***
   12d50:  b13ffc1f  cmn   x0, #0xfff          # Check if error
   12d54:  540000e3  b.cc  12d70               # If X0 >= -4095, success
   # Error path:
   12d58:  92800004  mov   x4, #-1             # r1 = -1
   12d5c:  f90023e4  str   x4, [sp, #64]       # Store r1 = -1
   12d60:  f90027ff  str   xzr, [sp, #72]      # Store r2 = 0
   12d64:  cb0003e0  neg   x0, x0              # errno = -X0 (negate)
   12d68:  f9002be0  str   x0, [sp, #80]       # Store errno
   12d6c:  d65f03c0  ret
   # Success path:
   12d70:  f90023e0  str   x0, [sp, #64]       # Store r1 = X0
   12d74:  f90027e1  str   x1, [sp, #72]       # Store r2 = X1
   12d78:  f9002bff  str   xzr, [sp, #80]      # Store err = 0
   12d7c:  d65f03c0  ret
```

## Syscall Calling Convention (Linux ARM64)

### Before SVC (User → Kernel Transition)

**Registers on entry to SVC**:
- X8 = syscall number (e.g., 56 for `openat`, 64 for `write`, 57 for `close`)
- X0 = arg1
- X1 = arg2
- X2 = arg3
- X3 = arg4
- X4 = arg5
- X5 = arg6

**SVC instruction**:
```assembly
d4000001  svc   #0x0
```

This causes a **synchronous exception** (Exception Class 0x15 - SVC).

### What the CPU Does on SVC

1. **Save state**:
   - SPSR_EL1 ← PSTATE (save processor state)
   - ELR_EL1 ← PC + 4 (save return address - next instruction after SVC)

2. **Switch to kernel**:
   - Switch from EL0 or EL1t to EL1h (exception mode)
   - SP ← SP_EL1 (use exception stack)
   - PC ← Exception vector (our exception handler)

3. **Mask interrupts**:
   - DAIF flags are set (disable interrupts during handler)

### After SVC (Kernel → User Transition)

**Return values** (Linux convention):
- X0 = result or negative errno
- X1 = second return value (rarely used, e.g., pipe() returns two fds)

**Error convention**:
- If X0 in range `[-4095, -1]` → ERROR, errno = -X0
- Otherwise → SUCCESS, return value = X0

This is what the `cmn x0, #0xfff` instruction checks!

```assembly
cmn x0, #0xfff    # Compare Negative: (X0 + 0xfff) and set flags
b.cc  success     # Branch if Carry Clear (unsigned >=)
```

If `X0 >= -4095` (unsigned comparison), it's success.
If `X0 < -4095` (in range [-4095, -1]), it's an error.

### Return to User

After handling the syscall, kernel executes **ERET**:

1. **Restore state**:
   - PSTATE ← SPSR_EL1
   - PC ← ELR_EL1 (return to instruction after SVC)

2. **Switch back to user**:
   - Switch from EL1h to EL1t (or EL0 if user mode)
   - SP ← SP_EL0 (back to original stack)

3. **Execution continues** at the instruction after SVC (0x12d50)

## Critical Differences from Function Calls

| Aspect | Function Call | Syscall (SVC) |
|--------|--------------|---------------|
| **Instruction** | `BL` (branch and link) | `SVC` (supervisor call) |
| **Transition** | Same privilege level | EL1t → EL1h (or EL0 → EL1h) |
| **Stack** | Same stack | SP_EL0 → SP_EL1 (different stack!) |
| **State Save** | LR (X30) only | SPSR_EL1, ELR_EL1 (CPU auto-saves) |
| **Return** | `RET` | `ERET` (exception return) |
| **Parameters** | X0-X7 (Go convention) | X8=syscall#, X0-X5=args (Linux convention) |
| **Interrupts** | Enabled | Masked during handler |

## Registers Across SVC

### Preserved by CPU Automatically

- **SPSR_EL1** - Saved processor state
- **ELR_EL1** - Return address (PC of instruction after SVC)
- **All general-purpose registers X0-X30** except modified by syscall handler

### Modified by Syscall

- **X0** - Return value or negative errno
- **X1** - Second return value (if any)
- **X8** - May be clobbered (used for syscall number)
- **Other registers** - Syscall handler may use X2-X18 as scratch

### Must Be Preserved by Our Handler

**Critical**: If our kernel handles the syscall, we must preserve:
- **X19-X29** - Callee-saved registers (if we use them)
- **X28 (g)** - Go's current goroutine pointer (NEVER modify!)
- **X30 (LR)** - Link register (caller's return address)
- **SP_EL0** - User's stack pointer (if we switch to SP_EL1)
- **FP (X29)** - Frame pointer (for stack unwinding)

## Example: os.Create() Syscall Trace

### High-Level Call

```go
f, err := os.Create("/tmp/test.txt")
```

### Syscall Trace

1. `os.Create()` → `os.OpenFile(name, O_RDWR|O_CREATE|O_TRUNC, 0666)`
2. `os.OpenFile()` → `syscall.Open(name, flags, mode)`
3. `syscall.Open()` → `syscall.Syscall(SYS_openat, ...)`
4. `syscall.Syscall()` → `runtime.entersyscall()` (notify runtime)
5. `syscall.Syscall()` → `syscall.RawSyscall6(SYS_openat, AT_FDCWD, name_ptr, flags, mode, 0, 0)`
6. `syscall.RawSyscall6()` → `internal/runtime/syscall.Syscall6.abi0()`
7. **SVC #0 - TRAP TO KERNEL**
8. Kernel handles openat syscall
9. **ERET - RETURN FROM KERNEL**
10. Check `cmn x0, #0xfff` - error or success?
11. If error: negate X0, return (r1=-1, r2=0, err=errno)
12. If success: return (r1=fd, r2=0, err=0)
13. `syscall.Syscall()` → `runtime.exitsyscall()` (notify runtime)
14. Return to `os.OpenFile()` with (fd, err)

## Parameters for Common Syscalls

### openat (syscall #56)
```
X8 = 56 (SYS_openat)
X0 = dirfd (usually AT_FDCWD = -100)
X1 = pathname (pointer to string)
X2 = flags (O_RDONLY, O_WRONLY, O_RDWR, O_CREATE, etc.)
X3 = mode (permissions if creating)
Returns: X0 = fd or negative errno
```

### write (syscall #64)
```
X8 = 64 (SYS_write)
X0 = fd
X1 = buffer (pointer)
X2 = count (bytes to write)
Returns: X0 = bytes written or negative errno
```

### close (syscall #57)
```
X8 = 57 (SYS_close)
X0 = fd
Returns: X0 = 0 or negative errno
```

### read (syscall #63)
```
X8 = 63 (SYS_read)
X0 = fd
X1 = buffer (pointer)
X2 = count (max bytes to read)
Returns: X0 = bytes read or negative errno
```

## Critical Insights for Our Kernel

### 1. SVC Uses Different Registers Than Function Calls

- Syscalls use X8 for syscall number (not part of normal function ABI)
- Parameters in X0-X5 (not X0-X7 like function calls)
- Return value only in X0 (and optionally X1)

### 2. Stack Pointer Changes on SVC

When SVC occurs:
- CPU switches from SP_EL0 (user/kernel stack) to SP_EL1 (exception stack)
- **This is automatic and unavoidable**

On ERET:
- CPU switches back from SP_EL1 to SP_EL0
- **SP_EL0 must have the same value as before SVC!**

### 3. Frame Pointer Must Be Preserved

Go code expects FP (X29) to point to the frame chain.
NOFRAME functions like the syscall wrappers rely on FP being correct.

If our exception handler modifies FP or doesn't restore it properly, the frame chain breaks and return values get written to the wrong location.

### 4. Return Address in ELR_EL1, Not LR

Unlike function calls (which save return address in X30/LR), syscalls save return address in **ELR_EL1**.

We must:
- Preserve ELR_EL1 during syscall handling
- Advance ELR_EL1 by 4 to skip the SVC instruction (or it will loop!)
- Restore ELR_EL1 before ERET

## Next Steps

Now that we understand the syscall interface, we can:

1. Verify our exception handler correctly:
   - Preserves FP (X29) across SVC
   - Preserves g (X28) across SVC
   - Advances ELR_EL1 by 4
   - Doesn't corrupt SP_EL0 (even though we use SP_EL1)

2. Check that our syscall handlers:
   - Receive parameters correctly from X0-X5, X8
   - Return values correctly in X0
   - Follow Linux errno convention (negative return = error)

3. Debug why return values aren't reaching the caller:
   - Where does FP point during syscall?
   - Where does FP point after ERET?
   - Are return values being written to the correct [FP+offset]?
