# QEMU Virt Machine Memory Layout Discovery

## Critical Finding

**QEMU's `virt` machine type for AArch64 has RAM starting at `0x40000000`, NOT at `0x0`!**

This discovery explains why:
- ✅ Code execution works (kernel loaded in ROM region at 0x200000)
- ✅ UART MMIO works (at 0x09000000)
- ❌ BSS writes failed (BSS was at 0x32f000 - not in RAM!)
- ❌ Write barrier flag couldn't be set (was in non-RAM region)

## Memory Map

### QEMU virt Machine (AArch64)

```
0x00000000 - 0x08000000:  Flash/ROM region
  0x00200000:             Kernel load address (_start)
  0x002xxxxx:             .text, .rodata, .data sections
  
0x09000000 - 0x09010000:  UART PL011 (MMIO)

0x40000000 - 0x60000000:  RAM (actual writable memory!)
  0x40000000:             __bss_start (BSS section)
  0x40026b40:             runtime.writeBarrier  
  0x4003c000:             __bss_end
  0x40400000:             Stack pointer
  0x40500000:             Heap start
```

### Raspberry Pi 4 (Real Hardware)

```
0x00000000:               Start of RAM
0x00200000:               Kernel load address
0x0032f000:               BSS start (in RAM)
0x00400000:               Stack pointer
```

## The Problem

Our original linker script placed BSS immediately after .data in the same memory region as the kernel code:

```ld
. = 0x200000;              // Kernel load address
.text : { ... }            // 0x200000 - code
.rodata : { ... }          // After code
.data : { ... }            // After rodata
.bss : { ... }             // 0x32f000 - PROBLEM: Not in RAM on QEMU virt!
```

On real Raspberry Pi hardware, 0x200000 IS in RAM, so this works fine. But on QEMU virt:
- 0x200000 is in **ROM** (read-only flash region for code)
- RAM doesn't start until **0x40000000**

## The Solution

Update the linker script to place BSS in the RAM region:

```ld
/* Kernel code in ROM region */
. = 0x200000;
.text : { ... }
.rodata : { ... }
.data : { ... }

/* BSS in RAM region */
. = 0x40000000;            // Jump to RAM base
.bss (NOLOAD) : {
    *(.bss)
    *(.noptrbss)           // Include noptrbss to avoid gaps
}
```

This creates two separate PT_LOAD segments:
1. ROM segment: 0x200000 (code, rodata, data)
2. RAM segment: 0x40000000 (BSS)

## Testing Results

### Before Fix (BSS in ROM region)
```
S!      <- Write barrier flag: FAILS
T2      <- Test 2: pointer assignment
0       <- Write barrier flag reads as 0
N       <- Pointer assignment FAILS
```

### After Fix (BSS in RAM region)
```
S=      <- Write barrier flag: SUCCESS ✅
T2      <- Test 2: pointer assignment
1       <- Write barrier flag reads as 1 ✅
P       <- Pointer assignment SUCCESS ✅
```

## Implementation Details

### boot.s Changes

1. **Stack pointer**: Changed from `0x400000` to `0x40400000` (in RAM)
2. **BSS clear**: Now clears `0x40000000 - 0x4003c000` (in RAM)
3. **Write barrier flag**: Set at `0x40026b40` (in RAM)

### kernel.go Changes

1. **Write barrier address**: Updated to `0x40026b40`
2. **Heap start**: Changed from `0x500000` to `0x40500000` (in RAM)

## Why This Wasn't Obvious

1. **UART writes worked**: MMIO at 0x09000000 is always accessible, so we had output
2. **Code executed**: The kernel code at 0x200000 runs fine (ROM is executable)
3. **BSS clear "worked"**: The clear loop ran without errors (writes to ROM are silently ignored in QEMU)
4. **No crash**: QEMU doesn't fault on writes to ROM, they just don't persist

The only symptom was that **reading back** written values returned 0 instead of the written value.

## Platform Differences

This is a **QEMU-specific issue**. Real Raspberry Pi hardware:
- Has RAM starting at 0x0
- Kernel at 0x200000 is in RAM
- BSS at 0x32f000 is in RAM
- No changes needed!

For QEMU virt machine, we need a separate memory layout because the machine model is different from physical Raspberry Pi hardware.

## Lessons Learned

1. **Test memory writes early**: Don't assume memory layout without testing
2. **Read back written values**: Silent failures are hard to debug
3. **Check machine-specific memory maps**: QEMU machine types have different layouts
4. **UART working ≠ RAM working**: MMIO and RAM are separate regions
5. **Exception level doesn't matter**: Even at EL1, ROM is still read-only

## References

- QEMU virt machine documentation: Memory starts at 0x40000000
- ARM AArch64 architecture: ROM vs RAM regions
- ELF PT_LOAD segments: Separate segments for code vs data

## Future Work

Consider using build tags or separate linker scripts for:
- `qemu` build: BSS at 0x40000000
- `rpi` build: BSS at 0x32f000 (after kernel code)

This would allow the same codebase to work on both platforms without manual changes.

## Debugging Process Summary

The debugging process that led to this discovery:

1. **Initial symptom**: Write barrier flag couldn't be set, pointer assignments failed
2. **First hypothesis**: `.noptrbss` section not properly loaded by QEMU
3. **Testing**: Added markers to test memory writes at various addresses
4. **Key discovery**: `.bss` writes worked, `.noptrbss` writes failed
5. **Further testing**: ALL memory writes after BSS clear returned 0
6. **Critical test**: Write to 0x500000 before any other code → FAILED
7. **Breakthrough**: Write to 0x40000000 → **SUCCESS!**
8. **Conclusion**: QEMU virt machine RAM is at 0x40000000

### Test Sequence That Revealed the Issue

```assembly
// Test at _start, before anything else:
movz x14, #0x50, lsl #16       // Write to 0x500000
mov w15, #0x42
str w15, [x14]
ldr w16, [x14]
// Result: w16 = 0 (FAIL - not in RAM)

movz x14, #0x4000, lsl #16     // Write to 0x40000000  
mov w15, #0x42
str w15, [x14]
ldr w16, [x14]
// Result: w16 = 0x42 (SUCCESS - this is RAM!)
```

### Why UART Misled Us

UART MMIO writes worked perfectly at 0x09000000, giving us output. This made us think memory was generally working. However:
- **MMIO writes** (to device registers) work in their dedicated region
- **RAM writes** (to variables/heap/stack) need to be in the RAM region
- These are **completely separate** address spaces in QEMU virt machine

## Test Output Explanation

Final successful test output:
```
S=      Boot started, write barrier flag set (= means success)
B       Before kernel_main
K       In kernel_main  
Hello!  UART working
...
T2      Test 2: Global pointer assignment
1       Write barrier flag reads as 1 (enabled)
A       After assignment attempt
P       heapSegmentListHead is NOT nil (assignment worked!)
...
Exit    Clean exit via semihosting
```

Exit code: **0** (semihosting clean exit)

## Commands to Test

```bash
cd /Users/iansmith/mazzy/src
make clean
make kernel-qemu.elf
make push-qemu
source ../enable-mazzy
runqemu-fb
```

Expected output: Kernel boots, write barrier works, clean exit.


