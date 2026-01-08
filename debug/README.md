# GDB Debugging Scripts for ADRP Issue

## Problem Summary

Kmazarin's runtime initialization crashes with a data abort when trying to access the g register (x28). The issue occurs at:

**Location:** `runtime.getHugePageSize` at `0xFFFFFFFF41836340`
**Faulting instruction:** `ldr x16, [x28, #16]` (loading `g.stackguard0`)
**x28 value:** `0xD65F03C0928004A0` (garbage - looks like ARM64 instructions)
**Expected x28:** `0xFFFFFFFF4197BF20` (g0 structure address)

## ⚠️ CRITICAL FINDING

**GDB debugging proved that ADRP works correctly!**

The g register IS set correctly by ADRP in `runtime.rt0_go.abi0`:
```asm
4186eb48:   b000087c    adrp    x28, 4197b000    # x28 = 0xFFFFFFFF4197B000 ✓
4186eb4c:   913c839c    add     x28, x28, #0xf20  # x28 = 0xFFFFFFFF4197BF20 ✓
```

**However, x28 gets corrupted later** between `getCPUCount` and `getHugePageSize`.

### What We Know:

✅ ADRP instruction works correctly (verified with `gdb-adrp-debug.gdb`)
✅ Page tables are correct (verified with `gdb-check-mappings.gdb`)
✅ g0 memory is accessible and properly zeroed
✅ First syscall succeeds (x28 must be valid)
✅ `getCPUCount` succeeds (x28 must be valid)
❌ **Something corrupts x28 before `getHugePageSize`**

## Investigation So Far

### What We've Verified

1. **ADRP instruction encoding is correct**
   - Instruction bytes: `0xB000087C`
   - immlo=1, immhi=0x43, imm21=0x10D
   - Page offset = 0x10D000
   - Should compute: `(PC & ~0xFFF) + 0x10D000`

2. **Code executes at high memory**
   - PC confirmed via QEMU monitor: `0xFFFFFFFF41894484`
   - Not executing at low memory

3. **g0 memory at high address is zeroed by Cardinal**
   - Cardinal output shows: `[g0=0000000000000000]`
   - Low-memory address (0x4197BF20) contains zeros
   - High-memory address (0xFFFFFFFF4197BF20) reports "Cannot access memory" via QEMU monitor

4. **All kmazarin segments are loaded and mapped in TTBR1**
   - RW segment: 0xFFFFFFFF41970000-0xFFFFFFFF419BB000
   - g0 address (0xFFFFFFFF4197BF20) is within this range

5. **Program headers are correctly relocated**
   - Entry point: 0xFFFFFFFF41871CB0 ✓
   - LOAD segments all at high memory (0xFFFFFFFF...)

### Theories

1. **QEMU bug with ADRP in high memory** - No specific bug found in searches
2. **Page table mapping issue** - Address should be mapped but QEMU says inaccessible
3. **Instruction corruption** - Relocation tool might be damaging ADRP instructions
4. **Sign extension issue** - ARM64 ADRP sign-extends 21-bit immediate, possible interaction with high memory?

## GDB Debugging Scripts

### Prerequisites

```bash
# Install gdb-multiarch (if not already installed)
brew install gdb  # macOS
# or
sudo apt-get install gdb-multiarch  # Linux

# Build cardinal and kmazarin
cd /Users/iansmith/mazzy
make clean && make cardinal
```

### Script 1: gdb-adrp-debug.gdb ✅ COMPLETED

**Purpose:** Break at the ADRP instruction and single-step to see what happens

**Result:** ✅ **ADRP works perfectly!** x28 is set correctly to 0xFFFFFFFF4197BF20

### Script 2: gdb-check-mappings.gdb ✅ COMPLETED

**Purpose:** Verify that 0xFFFFFFFF4197BF20 is actually mapped in the page tables

**Result:** ✅ **Address is properly mapped!** All page table levels present, memory accessible

### Script 3: gdb-narrow-corruption.gdb 🔍 **RUN THIS NEXT**

**Purpose:** Find exactly where x28 gets corrupted

**Terminal 1 - Start QEMU with GDB server:**
```bash
~/mazzy/bin/qemu-system-aarch64 \
    -S -s \
    -M virt,virtualization=off \
    -cpu cortex-a72 \
    -m 8G \
    -kernel build/cardinal.elf \
    -nodefaults \
    -device bochs-display \
    -display none \
    -serial stdio \
    -no-reboot
```

**Terminal 2 - Run GDB:**
```bash
cd /Users/iansmith/mazzy
gdb-multiarch -x debug/gdb-adrp-debug.gdb build/kmazarin.elf
```

**Alternative - Load script after starting GDB:**
```bash
cd /Users/iansmith/mazzy
gdb-multiarch build/kmazarin.elf
(gdb) source debug/gdb-adrp-debug.gdb
```

**What to look for:**
- Does x28 get set correctly by ADRP?
- What value does x28 have after ADRP?
- Does the ADD instruction work correctly?
- Can we read from the g0 address after x28 is set?

### Script 2: gdb-trace-entry.gdb

**Purpose:** Trace execution from kmazarin entry to the ADRP instruction

**Terminal 1 - Start QEMU with GDB server:**
```bash
~/mazzy/bin/qemu-system-aarch64 \
    -S -s \
    -M virt,virtualization=off \
    -cpu cortex-a72 \
    -m 8G \
    -kernel build/cardinal.elf \
    -nodefaults \
    -device bochs-display \
    -display none \
    -serial stdio \
    -no-reboot
```

**Terminal 2 - Run GDB:**
```bash
cd /Users/iansmith/mazzy
gdb-multiarch -x debug/gdb-trace-entry.gdb build/cardinal.elf
```

**What to look for:**
- Does execution flow correctly from _rt0_arm64_linux -> main -> rt0_go?
- What is x28's value before the ADRP?
- Does the ADRP instruction decode correctly?
- What is the calculated target address vs actual x28 value?

### Script 3: gdb-check-mappings.gdb

**Purpose:** Verify that 0xFFFFFFFF4197BF20 is actually mapped in the page tables

**Terminal 1 - Start QEMU with GDB server:**
```bash
~/mazzy/bin/qemu-system-aarch64 \
    -S -s \
    -M virt,virtualization=off \
    -cpu cortex-a72 \
    -m 8G \
    -kernel build/cardinal.elf \
    -nodefaults \
    -device bochs-display \
    -display none \
    -serial stdio \
    -no-reboot
```

**Terminal 2 - Run GDB:**
```bash
cd /Users/iansmith/mazzy
gdb-multiarch -x debug/gdb-check-mappings.gdb
```

**What to look for:**
- Are all 4 page table levels (L0-L3) present?
- What are the permissions on the final page?
- Can GDB actually read from 0xFFFFFFFF4197BF20?
- If not mapped, why not? Cardinal should have mapped it.

## Manual GDB Commands

If you want to debug manually instead of using the scripts:

```gdb
# Connect to QEMU
target remote localhost:1234

# Break at ADRP
break *0xFFFFFFFF4186EB48

# Continue to breakpoint
continue

# Examine instruction
x/1i $pc
x/1xw $pc

# Check x28 before
info register x28

# Single-step
stepi

# Check x28 after
info register x28

# Decode ADRP manually
set $insn = *(uint32_t*)0xFFFFFFFF4186EB48
print/x $insn
print/x ($insn >> 5) & 0x7FFFF

# Try to read from g0 address
x/8gx 0xFFFFFFFF4197BF20

# Check TTBR1
info register ttbr1_el1

# Walk page tables manually
x/8gx ($ttbr1_el1 & ~0xFFF)
```

## Expected Outcomes

### If ADRP works correctly:
- x28 = 0xFFFFFFFF4197B000 after ADRP
- x28 = 0xFFFFFFFF4197BF20 after ADD
- Can read from 0xFFFFFFFF4197BF20
- System continues without crash

### If ADRP is buggy:
- x28 = garbage (0xD65F03C0928004A0 or similar)
- Cannot read from address in x28
- System crashes at getHugePageSize

## Next Steps

Based on GDB findings:

1. **If ADRP sets x28 incorrectly:**
   - Check QEMU version and report bug to QEMU project
   - Try different QEMU version or TCG vs KVM
   - Workaround: Have Cardinal pre-set g register before jumping

2. **If address is not mapped:**
   - Debug Cardinal's page table setup
   - Check why TTBR1 entries are missing for that address
   - Verify segment loading code in kernel.go

3. **If instruction is corrupted:**
   - Debug relocation tool (tools/relocate-kmazarin.go)
   - Compare instruction bytes before/after relocation
   - Check if relocation is incorrectly modifying ADRP

## References

- [ARM ADRP Instruction Spec](https://www.scs.stanford.edu/~zyedidia/arm64/adrp.html)
- [ADRP & LDR in arm64/kernel](https://duetorun.com/blog/20230612/arm64-kernel-adrp-ldr/)
- [The AArch64 processor, part 11: Loading addresses](https://devblogs.microsoft.com/oldnewthing/20220809-00/?p=106955)
- [ARM64 Memory Layout](https://www.kernel.org/doc/html/latest/arch/arm64/memory.html)
