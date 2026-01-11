# Next Steps: Debugging and Completing Thread Context Switching

## Current Status

**Working:**
- ✅ Thread creation via clone
- ✅ Thread yielding in nanosleep/sched_yield
- ✅ Context switching infrastructure active
- ✅ AsyncPreempt configured correctly
- ✅ No more infinite busy-wait loop

**Broken:**
- ❌ Crash after futex operations: `R![0000000000000030]FAIL x28=FFFFFFFF4197BF20`
- ❌ System crashes during futex/page-fault interaction

## Immediate Priority: Fix the Crash

### 1. Understand the Crash Location

**Tools to Use:**
```bash
# Run with longer timeout to capture more context
bin/claude/run 10

# Watch live output
tail -f /tmp/cardinal-serial.log

# Search for specific error patterns
grep -a "FAIL\|panic\|crash" /tmp/cardinal-serial.log

# Find where error code 0x30 (48 decimal) is printed
grep -rn "0x30\|!\[" src/
```

**Error Code Analysis:**
- Output: `R![0000000000000030]FAIL x28=FFFFFFFF4197BF20`
- `0x30` = 48 decimal = likely an error code or trap number
- `x28=FFFFFFFF4197BF20` = g register value (runtime.g0 address)
- `R![]FAIL` pattern suggests an exception or trap handler

**What to Search:**
1. Where is `![` error pattern printed? Search all `.s` assembly files
2. What exception/trap handler prints error codes in brackets?
3. Is this in Cardinal or Kmazarin exception handlers?

**Claude Tools:**
```
Grep - Search for error patterns across codebase
Read - Examine specific exception handler files
Bash - Run grep commands for complex searches
```

### 2. Check Thread Context Switching Logic

**Hypothesis:** Context switch after futex may be corrupting thread state.

**Files to Examine:**
- `src/kmazarin/golang/kmazarin/exceptions_arm64.s` - SVC handler, context switch
- `src/kmazarin/golang/kmazarin/threads.go` - DoContextSwitch, SaveContextFromFrame
- `src/kmazarin/golang/ksyscall/futex.go` - ThreadBlockFutex, ThreadWakeFutex

**What to Check:**
1. **Stack pointer correctness:**
   ```bash
   # Look for SP corruption in context switch
   grep -n "SP_EL0\|SP\|RSP" src/kmazarin/golang/kmazarin/exceptions_arm64.s
   ```

2. **Register save/restore:**
   ```bash
   # Check if all registers properly saved/restored
   grep -n "EXC_FRAME\|MOVD.*RSP" src/kmazarin/golang/kmazarin/exceptions_arm64.s
   ```

3. **g register (X28) handling:**
   - The error shows `x28=FFFFFFFF4197BF20` (runtime.g0)
   - After context switch, X28 must point to new thread's g
   - Check: Does DoContextSwitch update X28?

**Claude Tools:**
```
Read - Read exception handler assembly
Grep - Search for X28/g register handling
Edit - Fix register save/restore if needed
```

### 3. Add More Debug Breadcrumbs

**Strategy:** Add debug output at every step of context switching to find exact failure point.

**Where to Add Breadcrumbs:**

**A. In DoContextSwitch (threads.go):**
```go
func doContextSwitchImpl(framePtr uint64, targetIdx int32) uint64 {
    uartPutsDirect("[CTX: save current]\r\n")
    // ... save current context

    uartPutsDirect("[CTX: switch to T")
    uartPutHex32Direct(uint32(targetIdx))
    uartPutsDirect("]\r\n")
    // ... load new context

    uartPutsDirect("[CTX: done]\r\n")
}
```

**B. In Exception Handler (exceptions_arm64.s):**
```asm
// After GetSyscallSwitchTarget
UART_PUTC_SAFE 'X'  // Context switch requested
// ... do context switch
UART_PUTC_SAFE 'Y'  // Context switch complete
```

**C. In ThreadFindReady (threads.go):**
```go
func ThreadFindReady() int32 {
    uartPutsDirect("[TFR: searching]\r\n")
    // ... search
    if found {
        uartPutsDirect("[TFR: found T")
        uartPutHex32Direct(uint32(idx))
        uartPutsDirect("]\r\n")
    }
}
```

**Claude Tools:**
```
Edit - Add debug output to existing functions
Read - Check current debug output patterns
Bash - Rebuild and test after adding breadcrumbs
```

### 4. Verify Page Table Consistency

**Hypothesis:** Page fault during context switch may be accessing unmapped memory.

**What to Check:**
1. **Each thread's stack is mapped:**
   ```bash
   # Check thread stack allocation
   grep -n "ThreadContext\|stack\|SP_EL0" src/kmazarin/golang/kmazarin/threads.go
   ```

2. **Page tables remain valid during switch:**
   - TTBR0_EL1 / TTBR1_EL1 should not change during thread switch
   - Only SP_EL0 and PC (ELR_EL1) change

3. **Clone allocates stack properly:**
   ```bash
   # Verify clone syscall stack setup
   grep -A50 "func SyscallClone" src/kmazarin/golang/ksyscall/clone.go
   ```

**Claude Tools:**
```
Read - Examine thread creation and stack allocation
Grep - Search for page table operations during context switch
```

### 5. Use QEMU Monitor for Live Debugging

**Setup:**
```python
# bin/claude/debug-qemu.py
import socket, time

def qemu_cmd(cmd):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.connect(('127.0.0.1', 4444))
    sock.settimeout(2)
    time.sleep(0.2)
    sock.recv(4096)  # Drain banner
    sock.send(f'{cmd}\n'.encode())
    time.sleep(0.5)
    result = sock.recv(8192).decode()
    sock.close()
    return result

# Get registers at crash
print(qemu_cmd('info registers'))

# Disassemble at PC
pc = 0x41234567  # Get from crash output
print(qemu_cmd(f'x/20i {hex(pc)}'))
```

**Claude Tools:**
```
Write - Create debug script
Bash - Run QEMU monitor commands
Read - Examine output
```

## Medium Priority: Complete Thread Support

### 6. Implement Proper Sleep Timing

**Current:** Nanosleep yields but ignores sleep duration.

**TODO:**
1. Parse timespec from nanosleep `req` parameter:
   ```go
   type timespec struct {
       tv_sec  int64
       tv_nsec int64
   }
   reqPtr := (*timespec)(unsafe.Pointer(uintptr(req)))
   ```

2. Convert to timer ticks
3. Use ThreadBlockSleep (already exists in threads.go)
4. Timer interrupt wakes threads when time expires

**Files to Modify:**
- `src/kmazarin/golang/ksyscall/nanosleep.go`

**Claude Tools:**
```
Read - Check existing ThreadBlockSleep implementation
Edit - Add timespec parsing to nanosleep
Bash - Test with real sleep durations
```

### 7. Implement Random Thread Selection

**Current:** ThreadFindReady uses round-robin.

**TODO:** Add random selection option for better fairness.

**Approach:**
```go
func ThreadFindReadyRandom() int32 {
    // Get random number from RNG device
    rand := getRandom() % numThreads

    // Search starting from random offset
    for i := int32(0); i < numThreads; i++ {
        idx := (rand + i) % numThreads
        if threads[idx].State == ThreadReady {
            return idx
        }
    }
    return -1
}
```

**Files to Modify:**
- `src/kmazarin/golang/kmazarin/threads.go`
- `src/kmazarin/golang/ksyscall/nanosleep.go` (add parameter to choose strategy)

**Claude Tools:**
```
Read - Check RNG device interface
Edit - Implement random selection
Bash - Test fairness with multiple threads
```

### 8. Enable Timer-Based Preemption

**Current:** Timer preemption infrastructure exists but may not be fully working.

**TODO:**
1. Verify timer IRQ actually fires
2. Check asyncPreempt injection works
3. Test preemption of long-running code

**Debug Approach:**
```go
// In kirq/timer.go - add breadcrumb
func TimerIRQHandlerPreemptable(...) PreemptInfo {
    uartPutsDirect("[TIMER]")
    // ... rest of handler
}
```

**Files to Check:**
- `src/kmazarin/golang/kirq/timer.go`
- `src/kmazarin/golang/kmazarin/exceptions_arm64.s` (IRQ handler)
- `src/cardinal/golang/main/exceptions.go` (handleTimerIRQ)

**Claude Tools:**
```
Edit - Add timer debug output
Bash - Run and check for [TIMER] breadcrumbs
Read - Verify timer configuration
```

## Long-Term Improvements

### 9. Remove DEBUG_STATE.md After Resolution

Once the crash is fixed, remove the debug notes file:
```bash
git rm DEBUG_STATE.md
```

### 10. Clean Up Debug Output

After fixing the crash, remove or disable verbose debug output:
- Syscall number printing `(XX)` in dispatch.go
- Context switch breadcrumbs
- Page fault spam

Consider adding a debug level flag:
```go
const debugLevel = 0  // 0=none, 1=errors, 2=info, 3=verbose

func debugPrint(level int, msg string) {
    if level <= debugLevel {
        uartPutsDirect(msg)
    }
}
```

### 11. Document Thread Switching Architecture

Create comprehensive documentation:
- How syscalls trigger context switches
- Thread state machine (READY → RUNNING → BLOCKED → READY)
- Exception handler flow for SVC with context switch
- Memory layout for thread stacks

**File to Create:**
- `docs/THREAD_SWITCHING.md`

## Claude's Available Tools for Debugging

### Code Navigation
- **Glob** - Find files by pattern: `**/*.s`, `**/*thread*.go`
- **Grep** - Search code: regex patterns, context lines (-A/-B/-C)
- **Read** - Read files with line numbers, offsets

### Code Modification
- **Edit** - Exact string replacement (preserves indentation)
- **Write** - Create new files or overwrite existing

### Build & Test
- **Bash** - Run commands:
  - `bin/claude/build` - Rebuild everything
  - `bin/claude/run N` - Start QEMU with N second timeout
  - `bin/claude/stop` - Kill QEMU
  - `tail -f /tmp/cardinal-serial.log` - Watch live output
  - `grep -a pattern /tmp/cardinal-serial.log` - Search output

### Advanced Debugging
- **Bash + Python** - QEMU monitor interaction
- **Bash + nm** - Symbol table inspection
- **Bash + objdump** - Disassembly

### Important Limitations
- **Cannot** use interactive commands (git rebase -i, etc.)
- **Must** quote paths with spaces
- **Should** use parallel tool calls when possible for speed

## Recommended Debugging Workflow

1. **Add breadcrumbs** at suspected failure points
2. **Rebuild** with `bin/claude/build`
3. **Run** with `bin/claude/run 10` (longer timeout)
4. **Examine** `/tmp/cardinal-serial.log` for patterns
5. **Iterate** - add more breadcrumbs near the failure
6. **Binary search** - narrow down exact instruction that fails
7. **Fix** the bug
8. **Clean up** debug output
9. **Commit** with clear description

## Key Insight for Claude

The crash happens **after** the system has made significant progress:
- EarlyInit completed successfully
- AsyncPreempt is correct
- Threads are switching
- Futex wake/wait executed

This means the **infrastructure is sound** - it's likely a **specific edge case** in:
1. Register state corruption during context switch
2. Stack pointer alignment issue
3. Page table entry corruption
4. g register (X28) not being updated for new thread

Focus on the **context switch code path** and add breadcrumbs to trace exactly where the crash occurs.
