# ARM64 Go Calling Convention Study

## Purpose

This directory contains test programs designed to systematically study how Go compiler generates ARM64 assembly for function calls. By understanding the actual patterns Go uses, we can correctly implement our exception handlers and syscall interface.

## What We're Looking For

For each test program, we need to analyze:

### 1. Stack Pointer (SP) Management
- How does SP change when entering a function?
- How much space is allocated for the stack frame?
- Is the stack frame size constant or variable?
- Where does SP point relative to the frame pointer?

### 2. Frame Pointer (FP / X29) Management
- Does every function set up a frame pointer?
- How is FP initialized (what's the relationship to SP)?
- When is FP saved/restored?
- What does FP point to (top of frame, bottom, or something else)?

### 3. Parameter Passing
- Where do parameters go (registers vs stack)?
- Which registers are used for different types (int64, string, pointer, etc.)?
- What happens when there are more parameters than registers?
- How are struct parameters passed?
- How are return values passed?

### 4. Register Saving (Caller vs Callee)
- Which registers are caller-saved (volatile)?
- Which registers are callee-saved (non-volatile)?
- Where are callee-saved registers stored (on stack, at what offset)?
- Does the pattern change based on function complexity?

### 5. Register Spilling
- When do registers get spilled to memory?
- Who allocates the spill space (caller or callee)?
- Where in the stack frame do spills go?
- Can we predict when spilling will occur?

### 6. Function Prologue Pattern
- What's the standard entry sequence?
- Does it vary based on function attributes (NOSPLIT, NOFRAME, etc.)?
- How early is FP set up?
- When are callee-saved registers saved?

### 7. Function Epilogue Pattern
- What's the standard exit sequence?
- How are callee-saved registers restored?
- How is the stack unwound?
- What's the relationship between epilogue and RET instruction?

### 8. Stack Layout
For each function, document the stack frame layout:
```
High Address (SP on entry)
+---------------------------+
| Return address (LR/X30)   | ← Often at FP+8
+---------------------------+
| Saved FP (X29)            | ← Often at FP+0
+---------------------------+
| Callee-saved registers    | ← X19-X28 if used
+---------------------------+
| Local variables           |
+---------------------------+
| Spill slots               |
+---------------------------+
| Outgoing parameters       | ← Parameters that don't fit in registers
+---------------------------+ ← SP on exit (current SP)
Low Address
```

### 9. Special Cases
- NOSPLIT functions (no stack split check)
- NOFRAME functions (no FP setup)
- Leaf functions (don't call other functions)
- Functions with defer
- Functions with closures

### 10. Exception/Syscall Implications
For our exception handler, we need to know:
- If we save all registers on exception entry, what do we need to restore?
- Where does Go expect SP to point when returning from a syscall?
- Where does Go expect FP to point?
- Can we safely use a different stack for exception handlers?
- What happens if SP_EL0 vs SP_EL1 gets confused?

## Test Programs

### test01_nested_calls/
**Purpose**: Study basic function call chain with varying parameters

**Functions**:
- `main()` → calls level1
- `level1(string, int64)` → calls level2
- `level2(int64, int64, string)` → calls level3
- `level3(string, string, int64, int64)` → calls level4
- `level4(int64, int64, int64, int64, int64, string)` → returns values

**What to observe**:
- How parameters are passed at each level (registers vs stack)
- How FP is set up at each level
- How SP changes at each level
- Pattern of register saves/restores
- Where return values go

### test02_noframe/ (planned)
**Purpose**: Study NOFRAME functions like sysMmap

### test03_nosplit/ (planned)
**Purpose**: Study NOSPLIT functions

### test04_leaf/ (planned)
**Purpose**: Study leaf functions (no calls)

### test05_many_params/ (planned)
**Purpose**: Study parameter passing when >8 parameters

### test06_return_values/ (planned)
**Purpose**: Study multiple return values

## How to Build and Analyze

For each test program:

```bash
cd testXX_name/
GOOS=linux GOARCH=arm64 go build -o test
GOOS=linux GOARCH=arm64 go build -gcflags='-S' main.go 2>&1 | tee asm_output.txt
~/mazzy/aarch64-none-elf/bin/aarch64-none-elf-objdump -d test > disasm.txt
```

Then analyze:
1. `asm_output.txt` - Go compiler's assembly output (before final assembly)
2. `disasm.txt` - Final machine code disassembly

Look for patterns in:
- How the stack frame is allocated (`sub sp, sp, #N`)
- How FP is set up (`mov x29, sp` or `add x29, sp, #N`)
- How parameters are moved around
- How callee-saved registers are saved (`stp x19, x20, [sp, #N]`)
- How the function returns

## ARM64 Register Quick Reference

```
X0-X7    - Parameter/result registers (caller-saved)
X8       - Indirect result location (caller-saved)
X9-X15   - Temporary registers (caller-saved)
X16-X17  - Intra-procedure-call temporary registers (caller-saved)
X18      - Platform register (reserved on some platforms)
X19-X28  - Callee-saved registers
X29      - Frame pointer (FP)
X30      - Link register (LR)
SP       - Stack pointer
```

## Expected Findings

Based on ARM64 AAPCS64 (Procedure Call Standard), we expect:
- First 8 integer/pointer parameters in X0-X7
- Additional parameters on stack
- Return values in X0-X7
- Stack grows downward (SP decrements)
- FP points to saved FP/LR pair at top of frame
- 16-byte stack alignment required

But Go may have its own conventions! We need to verify.

## Critical Questions to Answer

1. **Where does SP_EL0 need to point when returning from a syscall?**
   - Same as when syscall was invoked?
   - Or does Go runtime expect it to change?

2. **What if we use a different stack (SP_EL1) for the exception handler?**
   - Can we safely switch stacks as long as we restore SP_EL0 before eret?

3. **Where does sysMmap write its return values?**
   - To `[FP+32]` and `[FP+40]` - but what if FP points to wrong stack?

4. **What registers MUST be preserved across a syscall?**
   - All callee-saved (X19-X28)?
   - FP (X29)?
   - LR (X30)?
   - What about X0-X18?

## Success Criteria

We'll know we understand the calling convention when we can:
1. Predict the exact stack layout of any function from its signature
2. Know exactly which registers to save/restore in our exception handler
3. Understand where SP_EL0 must point on syscall return
4. Implement a working exception handler that preserves Go's expectations
5. Successfully return from syscalls without corrupting the stack or registers
