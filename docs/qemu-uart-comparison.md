# QEMU UART Configuration Comparison

## Research Findings

Based on web search results, here are standard practices for UART initialization and QEMU configuration:

### Standard UART Initialization Pattern (C Code)

```c
#define UART_BASE 0x101f1000  // VersatilePB example
#define UART_DR   (*(volatile unsigned int *)(UART_BASE + 0x00))
#define UART_FR   (*(volatile unsigned int *)(UART_BASE + 0x18))

void uart_send(char c) {
    // Wait until transmit FIFO is not full (bit 5 = TXFF)
    while (UART_FR & (1 << 5)) {}
    UART_DR = c;
}
```

**Key Points:**
- Check bit 5 of FR register (TXFF - Transmit FIFO Full)
- Wait while bit 5 is set (FIFO full)
- Write to DR register when FIFO has space

### Standard QEMU Configuration

```bash
qemu-system-arm -M versatilepb -nographic -kernel kernel.elf -serial stdio
```

**Key Flags:**
- `-nographic`: Disables graphics, routes serial to console
- `-serial stdio`: Redirects first UART to stdio
- `-append "console=ttyAMA0"`: Linux kernel console (not needed for bare-metal)

### Our Current Configuration

**UART Address:** `0x09000000` (from `uart_qemu.go`)
- This is correct for QEMU `virt` machine type
- **Question:** Is this correct for `raspi4b` machine type?

**QEMU Command (from runqemu-fb):**
```bash
qemu-system-aarch64 -M raspi4b \
    -kernel /mnt/builtin/kernel.elf \
    -append "console=ttyAMA0,115200 earlyprintk" \
    -serial stdio \
    -vnc "0.0.0.0:0,password=off" \
    -no-reboot
```

**Our UART Code Pattern:**
```go
const uartBase uintptr = 0x09000000
const uartFR = uartBase + 0x18
const uartDR = uartBase + 0x00

// Wait for transmit FIFO to have space (bit 5 = TXFF)
for mmio_read(uartFR)&(1<<5) != 0 {
    // Wait
}
// Write character
mmio_write(uartDR, uint32(c))
```

## Potential Issues

### 1. Machine Type vs UART Address Mismatch

**Problem:** We're using:
- Machine type: `raspi4b` (Raspberry Pi 4B)
- UART address: `0x09000000` (QEMU virt machine UART)

**Raspberry Pi 4B UART:**
- Real hardware: PL011 UART at `0xFE201000` (PERIPHERAL_BASE + 0x201000)
- QEMU raspi4b: May use different address or device

**QEMU virt machine:**
- PL011 UART at `0x09000000` (standard QEMU virt machine)

### 2. Serial Configuration

**Standard pattern:**
```bash
-serial stdio          # First UART to stdio
-nographic             # Disable graphics (routes serial to console)
```

**Our pattern:**
```bash
-serial stdio          # First UART to stdio
-vnc ...               # VNC display (graphics enabled)
```

**Potential issue:** With VNC enabled, serial might not route to stdio properly.

### 3. Alternative: Use `-serial mon:stdio`

For bare-metal kernels, some sources recommend:
```bash
-serial mon:stdio      # Monitor + stdio (better for bare-metal)
```

Instead of:
```bash
-serial stdio          # Direct stdio (may not work for all machine types)
```

## Recommendations

### Option 1: Use QEMU `virt` Machine Type

If we want to use UART at `0x09000000`:
```bash
qemu-system-aarch64 -M virt \
    -cpu cortex-a72 \
    -m 512M \
    -kernel kernel.elf \
    -serial stdio \
    -nographic
```

**Pros:**
- UART address matches our code (`0x09000000`)
- Well-documented and standard
- Serial output works reliably

**Cons:**
- Not Raspberry Pi 4B specific
- May not match real hardware behavior

### Option 2: Fix UART Address for raspi4b

If we want to use `raspi4b` machine type:
- Need to determine correct UART address for QEMU's raspi4b
- May need to use Raspberry Pi 4B UART address (`0xFE201000`)
- Or check QEMU documentation for raspi4b UART mapping

### Option 3: Try `-serial mon:stdio`

For bare-metal kernels, try:
```bash
-serial mon:stdio
```

Instead of:
```bash
-serial stdio
```

This may work better for bare-metal kernels that don't use Linux-style console.

## Test Results

### ✅ Success with `virt` Machine Type

**Configuration that works:**
```bash
qemu-system-aarch64 -M virt \
    -cpu cortex-a72 \
    -m 512M \
    -kernel kernel.elf \
    -serial stdio \
    -display none \
    -no-reboot
```

**Results:**
- ✅ UART output works correctly
- ✅ Kernel executes and produces output
- ✅ All tests pass except T2 (write barrier test)

**Output observed:**
```
Hello!
MEM
[FC] Y Y
Initializing memory...
T1
Y
T2
N          ← Write barrier test FAILED
T3
Y
...
```

**Key Finding:**
- The write barrier patching tool successfully redirected 334 call sites
- However, the T2 test shows "N" (failed) instead of "P" (passed)
- This indicates the patching works, but the write barrier implementation needs investigation
- The issue is likely in the write barrier function itself (register values, memory address, or assignment logic)

### ❌ `raspi4b` Machine Type Issues

- UART at `0x09000000` doesn't work with `raspi4b` machine type
- No serial output appears
- Kernel executes (confirmed via trace) but output doesn't reach stdio

## Next Steps

1. ✅ **Test with `virt` machine type** - COMPLETED, works correctly
2. **Investigate write barrier implementation** - T2 test failing suggests register/memory issue
3. **Update runqemu-fb to use `virt` machine type** for better compatibility
4. **Debug write barrier function** - Check if x27 and x2 registers are correct when called

## References

- QEMU User Documentation: https://www.qemu.org/docs/master/system/qemu-manpage.html
- Standard bare-metal UART examples use `-nographic -serial stdio`
- Some bare-metal examples use `-serial mon:stdio` for better compatibility
- UART address `0x09000000` is standard for QEMU `virt` machine type

