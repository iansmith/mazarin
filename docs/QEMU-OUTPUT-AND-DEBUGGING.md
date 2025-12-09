# QEMU Output Issues and Debugging Techniques

## Why We Didn't Use mazboot During DMA Investigation

### The Need for Custom Debug QEMU

During DMA debugging, we needed to see QEMU's internal behavior. This required:

1. **Building custom QEMU** with fprintf debug statements
   - Location: `/tmp/qemu-source/build/qemu-system-aarch64`
   - mazboot uses system QEMU at `/opt/homebrew/bin/qemu-system-aarch64`
   - Can't easily substitute custom QEMU in script

2. **Separated output streams**
   - QEMU fprintf debug → stderr
   - Kernel UART output → stdout
   - Need to capture separately: `> kernel.log 2> qemu-debug.log`
   - mazboot mixes these in terminal

3. **Iteration speed**
   - Quick edits to QEMU source, rebuild, test
   - Direct command line faster than modifying script
   - No script overhead or parsing issues

## Output Stream Issues

### Stream Routing in QEMU

| Component | Default Stream | Content |
|-----------|---------------|---------|
| Kernel UART (PL011) | stdout | Kernel uartPuts messages |
| QEMU errors | stderr | "Error:", "Warning:" |
| QEMU fprintf debug | stderr | Custom debug prints |
| QEMU monitor | special | Interactive commands |

### Serial Configuration Impact

**`-serial stdio`**
- Direct connection: UART → stdout
- Simple but limited
- Cannot multiplex with QEMU monitor
- All debug output mixed

**`-serial mon:stdio`** (your improvement)
- Multiplexed connection
- UART + monitor both accessible
- Switch with Ctrl+A c
- Better for interactive use
- Still mixes streams in terminal

**`-nographic`**
- No GUI, terminal only
- Good for scripts and automation
- All output to terminal

### The Debug QEMU Use Case

When debugging QEMU itself:
```bash
# Separate streams completely
/tmp/qemu-source/build/qemu-system-aarch64 \
  -M virt -cpu cortex-a72 -m 1G \
  -kernel kernel.elf \
  -device ramfb \
  -nographic \
  -semihosting -semihosting-config target=native \
  < /dev/null \
  > kernel-uart.log \  # Kernel messages only
  2> qemu-debug.log     # QEMU debug only

# Then examine separately
cat kernel-uart.log    # Clean kernel output
grep "FW_CFG" qemu-debug.log  # Just the debug we added
```

This is why we ran QEMU directly - couldn't easily do this through mazboot.

## Your mazboot Improvements

### Change 1: `-kernel` instead of `-device loader`

**Before:**
```bash
-device loader,file="$KERNEL_PATH",cpu-num=0
```

**After:**
```bash
-kernel "$KERNEL_PATH"
```

**Why better:**
- Simpler syntax
- QEMU standard way to load kernels
- Handles ELF parsing automatically
- Less prone to errors

### Change 2: `-serial mon:stdio` instead of `-serial stdio`

**Before:**
```bash
-serial stdio
```

**After:**
```bash
-serial mon:stdio
```

**Why better:**
- Can access QEMU monitor (Ctrl+A c)
- Monitor useful for:
  - `info qtree` - see device tree
  - `info mtree` - see memory map
  - `quit` - clean shutdown
- More professional/standard

## When to Use What

### Use mazboot When:
- Normal kernel development
- Testing ramfb display
- Want GUI window (Cocoa)
- Standard testing workflow

### Run QEMU Directly When:
- Debugging QEMU itself (custom build)
- Need separated output streams
- Testing specific flags
- Automating with scripts

### Use Debug QEMU When:
- QEMU behavior is mysterious
- Need to see internal handler calls
- Verify values QEMU receives
- Understand MemoryRegion dispatch

## Example: Our DMA Debugging Session

```bash
# 1. Add debug to QEMU source
cd /tmp/qemu-source
vi hw/nvram/fw_cfg.c  # Add fprintf statements

# 2. Rebuild
cd build
PATH=$PATH:/opt/homebrew/bin ninja

# 3. Test with separated output
/tmp/qemu-source/build/qemu-system-aarch64 -M virt ... \
  > /dev/null 2>&1 | grep "FW_CFG_DMA"

# 4. See what QEMU receives
FW_CFG_DMA_WRITE: addr=0x0 value=0x40126c00 size=8
FW_CFG_DMA_TRANSFER: dma_addr=0x40126c00  ← Correct!
```

This revealed the byte-swapping issue that wasn't visible through kernel-side debugging.

## Troubleshooting Output Issues

### Problem: No kernel output visible

**Check:**
1. Is `-serial` specified? (Need stdio or mon:stdio)
2. Is UART initialized? (Should see early boot messages)
3. Is output going to wrong stream?

**Solution:**
```bash
# Force all output to terminal
qemu-system-aarch64 ... -serial mon:stdio 2>&1
```

### Problem: Debug output mixed/garbled

**Solution:**
```bash
# Separate streams
qemu ... > kernel.log 2> debug.log

# Or filter in real-time
qemu ... 2>&1 | grep -v "FW_CFG"  # Hide QEMU debug
```

### Problem: Can't see QEMU debug fprintf

**Check:**
1. Is debug QEMU being used? (`which qemu-system-aarch64`)
2. Is output going to stderr? (Redirect: `2>&1`)
3. Are strings in binary? (`strings qemu-system-aarch64 | grep DEBUG`)

**Solution:**
```bash
# Verify debug binary
strings /tmp/qemu-source/build/qemu-system-aarch64 | grep "FW_CFG_DMA"

# Capture stderr explicitly  
/tmp/qemu-source/build/qemu-system-aarch64 ... 2>&1 | tee full-output.log
```

## Conclusion

mazboot is perfect for normal development. We bypassed it during DMA investigation because we needed:
- Custom debug QEMU build
- Separated output streams
- Precise control over flags

Your improvements to mazboot (`-serial mon:stdio`, `-kernel`) make it even better for standard use.




