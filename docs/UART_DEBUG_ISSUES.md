# UART Debug Output Issues and Fixes

## Issue 1: Debug Wrapper Output (`G0:87#40[...]`)

### Problem
The output shows debug wrappers around text:
```
G0:87#40[main]main.gcMonitorLoopG0:87#40[(](G0:87#40[)])
G0:87#40[0x00]0x000000005efffd40G0:87#40[: ]:
```

### Analysis
The pattern `G0:87#40[...]` appears to be:
- **G0**: Goroutine 0 (g0 - the system goroutine)
- **87**: Possibly exception level or interrupt status (0x87 = 135 decimal)
- **#40**: Likely EL1 exception level (0x40) or ASCII '@'
- **[...]**: The actual text being printed, wrapped in brackets

### Likely Source
This is coming from the **UART interrupt handler** or exception handler adding context information to each character/chunk being transmitted. This happens when:
1. UART uses interrupt-driven transmission
2. Exception handler wraps output with debugging context
3. Goroutine scheduler adds context to print statements

### Solution
**Option 1: Disable UART Interrupt Debug Wrapping**
Find and comment out code in `uart_qemu.go` or exception handlers that adds goroutine/exception context.

**Option 2: Disable Interrupt-Driven UART During Boot**
Use direct UART output instead of ring buffer during early boot:
```go
// In uart_qemu.go, modify uartPutc to use direct output:
func uartPutc(c byte) {
    // Direct UART write instead of ring buffer
    asm.UartPutcPl011(c)
}
```

**Option 3: Find and Disable the Debug Flag**
Search for where this wrapping is added. Likely in:
- `UartTransmitHandler()` in uart_qemu.go
- Exception handler in exceptions.s or exceptions.go
- Go runtime print wrappers

## Issue 2: Extra Carriage Returns in Print Output

### Problem
Go runtime stacktraces show double line breaks:
```
main.gcMonitorLoop

    (empty line)
    /Users/iansmith/mazzy/...
```

### Root Cause
**Carriage Return Mismatch:**
1. Go runtime's `print()` adds `\n` (LF only)
2. Our UART code might convert `\n` to `\r\n` (CRLF)
3. If code explicitly adds `\r\n`, we get double conversion: `\r\n` → `\r\r\n`

**Or vice versa:**
1. Our code adds `\r\n`
2. Terminal/QEMU expects `\n` only
3. Extra `\r` causes formatting issues

### Solution: Standardize Newline Handling

**Fix 1: Make UART Consistent**
Choose ONE newline convention:

**Option A: Use `\n` everywhere, convert in UART layer**
```go
// In uart_qemu.go or uart output function:
func uartPutc(c byte) {
    if c == '\n' {
        // Auto-add CR before LF
        asm.UartPutcPl011('\r')
    }
    asm.UartPutcPl011(c)
}
```

Then in all code, use only `\n`:
```go
print("message\n")  // NOT "\r\n"
```

**Option B: Use `\r\n` everywhere, no conversion**
```go
// Remove any auto-conversion in UART
func uartPutc(c byte) {
    asm.UartPutcPl011(c)  // No conversion
}
```

Then in all code, explicitly use `\r\n`:
```go
print("message\r\n")
```

**Fix 2: Check Go Runtime Print Functions**
The Go runtime's print functions (used for panic, stack traces) might add their own line breaks. Need to ensure they match our convention.

## Recommended Immediate Fix

### Step 1: Disable Debug Wrapping (if found)
Search for code that adds `G0:87#40[...]` pattern and comment it out.

### Step 2: Fix Newline Convention
1. Pick **Option A** (use `\n`, convert in UART)
2. Modify `uartPutc()` in `src/mazboot/golang/main/uart_qemu.go`:

```go
//go:nosplit
func uartPutc(c byte) {
    // Auto-convert LF to CRLF for proper terminal display
    if c == '\n' {
        // Enqueue CR before LF
        uartEnqueueOrOverflow('\r')
    }

    // Enqueue the character
    if uartEnqueueOrOverflow(c) {
        // Enable TX interrupt
        asm.MmioWrite(QEMU_UART_BASE+0x38, 1<<5)
    }
}
```

3. Update all code to use `\n` instead of `\r\n`:
```bash
# Search and replace
find src/mazboot/golang/main -name "*.go" -exec sed -i '' 's/\\r\\n/\\n/g' {} \;
```

### Step 3: Test
```bash
make && NOGRAPHIC=1 timeout 10 ~/mazzy/bin/run-mazboot 2>&1 | head -100
```

## Quick Debug: Where is the Wrapper Coming From?

Add this to find the source:
```bash
# Search for the pattern in source
grep -r "G0:" src/mazboot/
grep -r "#40" src/mazboot/golang/main/*.go
grep -r "87#" src/mazboot/

# Check if it's in assembly
grep -r "G0\|#40\[" src/mazboot/asm/
```

If not found, it's likely being generated dynamically by the exception/interrupt handler or Go runtime.
