# x86_64 Kmazarin CS=0x38 Bug - Continuation Prompt

## Current Status (2025-02-11)

### ✅ What Works
- **Diplomat boots successfully** - UEFI → ELF loading → page table setup
- **CS switched from 0x38 to 0x08** - Far return works correctly (confirmed by serial: "IDT CS selector: 0x8")
- **Kmazarin initializes** - Runtime, VirtIO GPU, VirtIO Block, timers all work
- **Framebuffer visible** - 1920x1080 display rendering correctly
- **Userspace programs launch** - dapope.elf and stdio.elf load successfully
- **Entry point bug fixed** - Debug print was clobbering RAX with 'K' (0x4B)

### ❌ The Problem

Kmazarin crashes with:
```
!0DE=00000038@00000000438B71F9
```

**Decoded:**
- `!0D` = Vector 13 (General Protection Fault)
- `E=00000038` = Error code 0x38 = trying to load segment selector 0x38
- `@438B71F9` = Faulting RIP = IRETQ instruction in `load_context_and_iretq`

**Root Cause:**
A ThreadContext structure has **CS=0x38** stored at offset 152. When `load_context_and_iretq` builds an IRETQ frame from this context, it tries to load CS=0x38, which is **beyond the GDT limit of 0x37**.

**GDT Layout (diplomat + kmazarin):**
```
0x00-0x07: Null descriptor (index 0)
0x08-0x0F: Ring 0 Code (index 1) ← correct CS
0x10-0x17: Ring 0 Data (index 2) ← correct SS
0x18-0x1F: Ring 3 Code (index 3)
0x20-0x27: Ring 3 Data (index 4)
0x28-0x37: TSS (16-byte system descriptor, indices 5-6)
---
0x38: OUT OF BOUNDS! ← This is what's in the ThreadContext
```

Selector 0x38 would be index 7, but the GDT only has 7 descriptors (indices 0-6), with limit 0x37.

### 🔍 Investigation Results

**From QEMU CPU log:**
1. 123 page faults occur (normal demand paging, all with CS=0x0008 ✓)
2. After 3rd page fault, context switch triggered
3. New context being loaded has **all GPRs = 0** (uninitialized/fresh context)
4. IRETQ tries to load CS=0x38 from this context → GPF

**Key Observations:**
- All execution shows CS=0x0008 (correct) - no CS=0x0038 in any interrupt frame
- The faulty ThreadContext.CS value doesn't come from saving an exception frame
- ThreadContext offsets verified correct (CS at offset 152, SS at offset 160)
- The value 0x38 = 56 decimal = exactly one past GDT limit
- Context with all zeros suggests it was never properly initialized

**Where CS Gets Set:**
- `SetupForCloneChild()` sets CS=kernelCS (0x08), SS=kernelSS (0x10) ✓
- `SetupForUserspace()` sets CS=userCS (0x1B), SS=userSS (0x23) ✓
- `SaveContextFromFrame()` copies CS from exception frame[17] - would be 0x08
- `CloneNeedsParentRegs` copies entire parent context including CS/SS

**Mystery:** Where does a ThreadContext get CS=0x38?

## 🎯 Next Steps to Debug

### Priority 1: Add Runtime Instrumentation

Add debug output to trace CS values at key points:

**A. In `SaveContextFromFrame()` (save_context_amd64.go):**
```go
func SaveContextFromFrame(t *Thread, framePtr uintptr) {
    // ... existing code ...
    t.Context.CS = frame[17]
    t.Context.SS = frame[20]

    // DEBUG: Print CS/SS when saving
    if t.Context.CS != 0x08 && t.Context.CS != 0x1B {
        console.KPrintf("WARN: Saved CS=0x%x (expected 0x08 or 0x1B) for TID %d\n",
                        t.Context.CS, t.TID)
    }
}
```

**B. In `load_context_and_iretq` (exceptions_amd64.s):**
Add debug output before IRETQ to print CS/SS values from context:
```asm
// Before line 911: MOVQ 160(R12), R13
// Print CS value from context
MOVW    $0x3F8, DX
MOVB    $'@', AX        // '@' = about to load context
OUTB
MOVQ    152(R12), R11   // CS value
// Print as 4 hex digits
... hex printing code ...
```

**C. In thread creation paths:**
Verify CS is set correctly in:
- `NewKernelThread()`
- `NewUserThread()`
- Clone child setup

### Priority 2: Check for Uninitialized Contexts

**Theory:** A thread is being switched to before its context is initialized.

Check:
1. Is there a thread pool with pre-allocated Thread structs?
2. Are ThreadContexts zero-initialized before use?
3. Could a thread be added to ready queue before SetupForCloneChild()?

Look at:
- `threadListData [threadArraySize]Thread` - static array, zero-initialized
- Thread allocation in `NewThread()` or similar
- Ready queue insertion timing

### Priority 3: Check for Memory Corruption

**Theory:** Something is overwriting ThreadContext.CS field.

Verify:
1. ThreadContext struct size = 176 bytes (21 uint64 fields)
2. No buffer overflows writing to Thread arrays
3. No pointer arithmetic errors calculating CS offset

Add assertions:
```go
func (ctx *ThreadContext) ValidateCS() {
    if ctx.CS != 0x08 && ctx.CS != 0x1B && ctx.CS != 0x18 && ctx.CS != 0x23 {
        panic(fmt.Sprintf("Invalid CS: 0x%x", ctx.CS))
    }
}
```

### Priority 4: Alternative Approaches

**A. Add GPF Handler:**
Catch the GPF and print full context details:
```go
func HandleGPF(errCode, rip uint64) {
    console.KPrintf("GPF: errCode=0x%x RIP=0x%x\n", errCode, rip)
    if errCode == 0x38 {
        console.KPrintln("ERROR: Attempted to load CS=0x38 (beyond GDT limit)")
        console.KPrintln("This is a bug in ThreadContext initialization")
        // Print thread list to find which thread has CS=0x38
    }
}
```

**B. Add Pre-IRETQ Validation:**
Before building IRETQ frame, validate CS/SS:
```asm
// In load_context_and_iretq before line 905
MOVQ    152(R12), R13   // Load CS
CMPQ    R13, $0x08      // Check if 0x08
JE      cs_ok
CMPQ    R13, $0x1B      // Check if 0x1B (Ring 3)
JE      cs_ok
CMPQ    R13, $0x18      // Check if 0x18 (Ring 3 code alt)
JE      cs_ok
// Invalid CS - print and halt
MOVW    $0x3F8, DX
MOVB    $'!', AX
OUTB
HLT
cs_ok:
```

## 🔬 Specific Investigation Commands

After adding instrumentation, run:

```bash
# Clean build and run with 10s timeout
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

$GO tool task clean
$GO tool task run-amd64 TIMEOUT=10

# Check logs
$GO tool safe-serial-read /tmp/diplomat-serial.log | grep -i "CS\|warn"
tail -50 /tmp/qemu-cpu.log

# Count exceptions before crash
grep -c "v=0e" /tmp/qemu-cpu.log  # Page faults
grep -c "v=0d" /tmp/qemu-cpu.log  # GPFs
```

## 📊 What We Know vs Don't Know

**Known:**
- ✓ CS=0x38 is stored in a ThreadContext at offset 152
- ✓ GDT limit is 0x37, so 0x38 is out of bounds
- ✓ Crash happens during context switch after 3rd page fault
- ✓ Context being loaded has all GPRs = 0 (fresh/uninitialized)
- ✓ All normal execution has CS=0x08 (correct)
- ✓ Constants kernelCS=0x08, userCS=0x1B are correct

**Unknown:**
- ❓ Which thread has CS=0x38 in its context?
- ❓ When/where does CS get set to 0x38?
- ❓ Is this an uninitialized context or corruption?
- ❓ Why does it only happen after 123 page faults?
- ❓ Is this the boot thread or a newly created thread?

## 🎬 Recommended Session Start

1. **Add debug output** to SaveContextFromFrame() to print any non-standard CS values
2. **Add validation** in load_context_and_iretq to catch CS != 0x08/0x1B before IRETQ
3. **Rebuild and run** with instrumentation
4. **Analyze logs** to find where CS=0x38 first appears
5. **Trace back** to the thread creation/initialization that missed setting CS

The key is to **catch the moment** when CS=0x38 is first written or when a context is used before being initialized.

## 📁 Relevant Files

- `kmazarin/kmazarin/thread_context_amd64.go` - ThreadContext struct, CS constants
- `kmazarin/kmazarin/save_context_amd64.go` - SaveContextFromFrame (copies CS from frame[17])
- `kmazarin/kmazarin/exceptions_amd64.s` - load_context_and_iretq (builds IRETQ frame)
- `kmazarin/kmazarin/threads.go` - Thread allocation, CloneNeedsParentRegs
- `diplomat/main/uefi_calls_amd64.s` - GDT setup (where we fixed the entry point bug)

Good luck! 🚀
