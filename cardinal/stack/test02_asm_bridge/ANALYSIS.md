# Analysis: Go ↔ Assembly Bridge Interface

## Summary

This test demonstrates the exact calling convention between Go code and Go assembly code, including parameter passing, return values, and stack frame management.

## Call Chain

```
main.main (Go)
    ↓ calls with (100, 42, "hello-from-go")
main.AsmBridge.abi0 (Assembly)
    ↓ calls with (200, 126, "hello-from-go", "from-asm")
main.GoHelper.abi0 (ABI wrapper)
    ↓
main.GoHelper (Go)
    ↓ returns (326, "hello-from-go+from-asm")
main.AsmBridge.abi0
    ↓ returns (426, "hello-from-go+from-asm")
main.main
```

## Key Finding: ABI Wrapper Functions

The Go compiler generated `.abi0` wrapper functions that we didn't write!

- `main.AsmBridge.abi0` - Wrapper around our assembly function
- `main.GoHelper.abi0` - Wrapper around the Go function

**Why?** These wrappers handle the transition between:
- **Register-based ABI** (new Go calling convention)
- **Stack-based ABI** (traditional calling convention)

## AsmBridge.abi0 Analysis (Our Assembly Function)

### Prologue (Lines a4cd0-a4cd8)

```assembly
a4cd0:  f81b0ffe  str   x30, [sp, #-80]!    # Push LR, allocate 80-byte frame
a4cd4:  f81f83fd  stur  x29, [sp, #-8]      # Save FP at [sp-8]
a4cd8:  d10023fd  sub   x29, sp, #0x8       # Set FP = SP - 8
```

**Standard Go prologue!** Even though we wrote NOSPLIT, it still sets up FP.

### Parameter Reception (Lines a4cdc-a4ce8)

```assembly
a4cdc:  f90007e0  str   x0, [sp, #8]        # Save param: a
a4ce0:  f9000be1  str   x1, [sp, #16]       # Save param: b
a4ce4:  f9000fe2  str   x2, [sp, #24]       # Save param: msg.ptr
a4ce8:  f90013e3  str   x3, [sp, #32]       # Save param: msg.len
```

**Parameters arrive in registers X0-X3:**
- X0 = a (int64) = 100
- X1 = b (int64) = 42
- X2 = msg.ptr (string pointer)
- X3 = msg.len (string length) = 13

### Computation (Lines a4cec-a4cfc)

```assembly
a4cec:  aa0003e4  mov   x4, x0              # x4 = a
a4cf0:  8b040084  add   x4, x4, x4          # x4 = a*2 = 200
a4cf4:  aa0103e5  mov   x5, x1              # x5 = b
a4cf8:  8b0100a5  add   x5, x5, x1          # x5 = b*2
a4cfc:  8b0100a5  add   x5, x5, x1          # x5 = b*3 = 126
```

Performs the arithmetic we requested.

### Calling GoHelper (Lines a4d00-a4d1c)

```assembly
a4d00:  aa0403e0  mov   x0, x4              # Param 1: x = a*2 = 200
a4d04:  aa0503e1  mov   x1, x5              # Param 2: y = b*3 = 126
a4d08:  f9400fe2  ldr   x2, [sp, #24]       # Param 3: s1.ptr = msg.ptr
a4d0c:  f94013e3  ldr   x3, [sp, #32]       # Param 4: s1.len = msg.len
a4d10:  f00002a4  adrp  x4, fb000           # Param 5: s2.ptr = "from-asm"
a4d14:  910ca084  add   x4, x4, #0x328
a4d18:  b27d03e5  orr   x5, xzr, #0x8       # Param 6: s2.len = 8
a4d1c:  94000009  bl    a4d40 <main.GoHelper.abi0>
```

**Parameters passed in X0-X5:**
- X0 = x (int64) = 200
- X1 = y (int64) = 126
- X2 = s1.ptr (string pointer)
- X3 = s1.len (string length)
- X4 = s2.ptr (string pointer to "from-asm")
- X5 = s2.len = 8

### Receiving Return Values (Lines a4d20-a4d28)

```assembly
a4d20:  f90017e0  str   x0, [sp, #40]       # Save return: sum
a4d24:  f9001be1  str   x1, [sp, #48]       # Save return: combined.ptr
a4d28:  f9001fe2  str   x2, [sp, #56]       # Save return: combined.len
```

**Return values arrive in X0, X1, X2:**
- X0 = sum (int64) = 326
- X1 = combined.ptr (string pointer)
- X2 = combined.len (string length)

### Preparing Our Return (Lines a4d2c-a4d30)

```assembly
a4d2c:  f94007e3  ldr   x3, [sp, #8]        # Load original a = 100
a4d30:  8b030000  add   x0, x0, x3          # result = sum + a = 426
```

X1 and X2 already contain the string (combined), so no need to move them.

### Epilogue (Lines a4d34-a4d3c)

```assembly
a4d34:  f85f83fd  ldur  x29, [sp, #-8]      # Restore FP
a4d38:  f84507fe  ldr   x30, [sp], #80      # Restore LR, free frame
a4d3c:  d65f03c0  ret
```

**Return values in X0, X1, X2:**
- X0 = result = 426
- X1 = tag.ptr
- X2 = tag.len

## GoHelper.abi0 Analysis (Wrapper)

### Prologue (Lines a4d40-a4d48)

```assembly
a4d40:  f81c0ffe  str   x30, [sp, #-64]!    # Push LR, allocate 64-byte frame
a4d44:  f81f83fd  stur  x29, [sp, #-8]      # Save FP
a4d48:  d10023fd  sub   x29, sp, #0x8       # Set FP = SP - 8
```

Standard prologue.

### Loading Parameters FROM STACK (Lines a4d4c-a4d60)

```assembly
a4d4c:  f94027e0  ldr   x0, [sp, #72]       # Load x from [sp+72]
a4d50:  f9402be1  ldr   x1, [sp, #80]       # Load y from [sp+80]
a4d54:  f9402fe2  ldr   x2, [sp, #88]       # Load s1.ptr
a4d58:  f94033e3  ldr   x3, [sp, #96]       # Load s1.len
a4d5c:  f94037e4  ldr   x4, [sp, #104]      # Load s2.ptr
a4d60:  f9403be5  ldr   x5, [sp, #112]      # Load s2.len
```

**CRITICAL OBSERVATION**: The wrapper loads parameters from **BEYOND the stack frame**!

- [sp+72] to [sp+112] are ABOVE the saved LR/FP
- These are in the CALLER's stack frame
- The caller (AsmBridge.abi0) put them there

Wait, that doesn't match! Let me check the call again...

Looking at a4d1c, we call with registers X0-X5. So the wrapper must be doing something else...

Actually, I think I misread. Let me re-examine...

### Actually: Parameters on Stack for Stack-Based ABI

The .abi0 wrapper receives parameters in **registers** from AsmBridge, but GoHelper (the actual Go function) expects them on the **stack** (stack-based ABI).

So the wrapper:
1. Receives params in X0-X5
2. Pushes them to stack
3. Calls GoHelper
4. GoHelper reads from stack
5. Wrapper receives return values from GoHelper
6. Returns them in registers to AsmBridge

But the disassembly shows loading FROM stack [sp+72]... This is confusing.

Let me check the actual GoHelper function (not the wrapper):

## GoHelper Analysis (Actual Go Function)

### Prologue (Lines a4b90-a4ba8)

```assembly
a4b90:  f9400b90  ldr   x16, [x28, #16]     # Stack limit check
a4b94:  d10043f1  sub   x17, sp, #0x10
a4b98:  eb10023f  cmp   x17, x16
a4b9c:  54000829  b.ls  a4ca0               # Call morestack if needed
a4ba0:  f8170ffe  str   x30, [sp, #-144]!   # Allocate 144-byte frame
a4ba4:  f81f83fd  stur  x29, [sp, #-8]      # Save FP
a4ba8:  d10023fd  sub   x29, sp, #0x8       # Set FP = SP - 8
```

### Loading Parameters (Lines a4bac-a4bc0)

```assembly
a4bac:  f9004fe0  str   x0, [sp, #152]      # Save param x at [sp+152]
a4bb0:  f90053e1  str   x1, [sp, #160]      # Save param y at [sp+160]
a4bb4:  f90057e2  str   x2, [sp, #168]      # Save param s1.ptr
a4bb8:  f90063e5  str   x5, [sp, #192]      # Save param s2.len
a4bbc:  f9005fe4  str   x4, [sp, #184]      # Save param s2.ptr
a4bc0:  f9005be3  str   x3, [sp, #176]      # Save param s1.len
```

**Parameters arrive in X0-X5!** This is the **register-based ABI**.

But [sp+152] is ABOVE the stack pointer (SP after allocating 144 bytes would be at original SP - 144). So [sp+152] = [original_SP + 8].

This means parameters are being saved to the **caller's stack frame** area (above the callee's frame).

### Return Values (Lines a4c90-a4c9c)

```assembly
a4c90:  f94023e0  ldr   x0, [sp, #64]       # Load sum
a4c94:  f85f83fd  ldur  x29, [sp, #-8]      # Restore FP
a4c98:  f84907fe  ldr   x30, [sp], #144     # Restore LR, free frame
a4c9c:  d65f03c0  ret
```

Before the epilogue, X0, X1, X2 contain:
- X0 = sum (from computation)
- X1 = combined.ptr (from concatstring3 call)
- X2 = combined.len (from concatstring3 call)

**Return values in X0, X1, X2** - register-based!

## Critical Insights

### 1. Go Now Uses Register-Based Calling Convention

Both Go functions and assembly functions pass parameters and return values in **registers**:
- Parameters: X0-X7 (and more if needed)
- Return values: X0-X7 (and more if needed)

### 2. Stack Frame Still Set Up

Even with register passing, every function:
- Sets up a stack frame (saves LR, FP)
- Has space for local variables
- May spill registers to stack

### 3. No Special Assembly Handling Needed

Our assembly function works EXACTLY like a Go function:
- Receives params in X0-X7
- Returns values in X0-X7
- Sets up FP and LR like normal

### 4. FP Must Be Preserved

Every function sets up FP, and it points to the saved FP location. This creates a frame chain for stack walking.

## Application to Our Exception Handler

For syscall handling, we now know:

### What We Must Preserve

1. **X19-X28** - Callee-saved registers (if we use them)
2. **X29 (FP)** - Frame pointer (CRITICAL!)
3. **X28 (g)** - Current goroutine pointer (NEVER modify!)
4. **SP** - Stack pointer (must be same before/after)

### What Gets Clobbered

1. **X0-X18** - Caller-saved (syscall can clobber except X0 = return value)
2. **X30 (LR)** - Return address (caller saves this)

### How sysMmap Works

```assembly
TEXT runtime·sysMmap(SB),NOSPLIT|NOFRAME,$0
    # Parameters in X0-X7
    MOVD  $SYS_mmap, X8
    SVC                         # Our exception handler
    # Return values:
    # X0 = pointer or error code
    CMN   $4095, X0
    BCC   ok
    # Error path
    NEG   X0, X0
    MOVD  $0, X0                # p = nil
    RET                         # err in X1? No, separate return
ok:
    # Success path
    MOVD  X0, X0                # p = X0
    MOVD  $0, X1                # err = 0
    RET
```

Wait, this doesn't match either. Let me look at the actual sysMmap code again...

Actually, from the earlier investigation, sysMmap is NOFRAME, meaning it doesn't set up FP. It uses the **caller's FP**.

So when sysMmap writes:
```
MOVD  R0, p+32(FP)
MOVD  $0, err+40(FP)
```

It's writing to `[FP+32]` and `[FP+40]`, which are in the **caller's** stack frame.

**If FP is wrong, these writes go to the wrong place!**

## The Real Problem

Our exception handler must preserve **FP** across the syscall. If we switch stacks (from kernel stack to exception stack), we must ensure FP still points to the correct location in the kernel stack.

**FP is not SP!** They are different registers:
- SP points to current stack (changes when we switch stacks)
- FP points to frame chain (must stay pointing to kernel stack)

When we switch from SP_EL0 (kernel stack) to SP_EL1 (exception stack), FP should remain unchanged, still pointing into the kernel stack.

The question is: **Are we accidentally modifying FP in our exception handler?**

Or is there a more subtle issue where the frame chain gets broken?

Next step: Check our exception handler to see if we're preserving FP correctly.
