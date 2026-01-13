# ADRP Investigation Summary

## Web Search Results

I searched for known issues with QEMU ARM64 ADRP instructions and high memory addresses (0xFFFFFFFF...). Here's what I found:

### QEMU-Specific Issues

No specific bugs were found related to ADRP execution with high-memory kernel addresses. The searches revealed:

- Various QEMU memory addressing limitations on certain platforms
- General ARM64 instruction bugs (UMOPA/UMOPS, SVE modes)
- ADRP disassembly issues in debugging tools (not execution bugs)
- Compiler/linker issues with ADRP relocations

**Sources:**
- [QEMU Issue Tracker](https://gitlab.com/qemu-project/qemu/-/issues) - No matching ADRP high-memory bugs
- [ARM ADRP Instruction Issues](https://groups.google.com/g/llvm-dev/c/I4BHnkDFo5w) - Linker/compiler problems, not QEMU

### ARM64 Architecture Insights

Found important documentation about how ADRP works:

#### ADRP Instruction Mechanics

From the [Stanford ARM64 Reference](https://www.scs.stanford.edu/~zyedidia/arm64/adrp.html):

> The 21-bit immediate (split into immhi and immlo fields) is concatenated with 12 zero bits, then **sign-extended to 64 bits**. This is added to PC<63:12>:0x000 to form the target address.

**Key formula:**
```
base = PC<63:12> : 0x000
imm = SignExtend64(immhi:immlo:0x000)
result = base + imm
```

#### Range and Usage

From [ADRP & LDR in arm64/kernel](https://duetorun.com/blog/20230612/arm64-kernel-adrp-ldr/):

- ADRP has a range of ±4 GB to the nearest 4 KB page
- Used for PC-relative address calculation before MMU is enabled
- In identity-mapped `.idmap.text` sections, VA=PA so ADRP returns physical addresses

From [The AArch64 processor, part 11](https://devblogs.microsoft.com/oldnewthing/20220809-00/?p=106955):

> ADRP loads the address of a nearby page. The reach of ADRP is ±4GB.

#### Kernel High Memory Addresses

From [ARM64 Memory Layout](https://www.kernel.org/doc/html/latest/arch/arm64/memory.html) and [Understanding 52-bit VA](https://opensource.com/article/20/12/52-bit-arm64-kernel):

- Kernel addresses use the high half: bits 63:48 = 1 (addresses start with 0xFFFF...)
- User addresses: bits 63:48 = 0
- TTBR0 handles user space, TTBR1 handles kernel space
- Kernel binaries must support both 48-bit and 52-bit addressing, requiring high addresses that are invariant across configurations

**This is normal and expected** - kmazarin using addresses starting with 0xFFFFFFFF is the standard kernel pattern.

### Sign Extension Analysis

The critical question: Does sign extension cause issues with high-memory PC values?

**Our case:**
- PC = 0xFFFFFFFF4186EB48
- PC page = 0xFFFFFFFF4186E000
- imm21 = 0x10D (positive, bit 20 = 0)
- imm21 << 12 = 0x10D000
- Sign-extended (from bit 32): 0x000000000010D000 (upper 32 bits = 0)

**Calculation:**
```
0xFFFFFFFF4186E000 +  // PC page (64-bit)
0x000000000010D000    // Sign-extended immediate (positive)
------------------
0xFFFFFFFF4197B000    // Correct target!
```

**This should work correctly.** The sign extension doesn't interfere because:
1. The offset (0x10D000) is positive (< 2GB)
2. Adding a positive 32-bit value to a 64-bit high-memory address works fine
3. ARM64 uses 64-bit addition, preserving the high bits

### Conclusion from Web Research

**No known QEMU or architecture bugs found** that would cause ADRP to fail with high-memory kernel addresses. The instruction should work correctly.

## Possible Root Causes (Ranked by Likelihood)

### 1. Page Table Mapping Issue (Most Likely)

**Evidence:**
- QEMU monitor says: `ffffffff4197bf20: Cannot access memory`
- Low-memory equivalent (0x4197BF20) IS accessible and contains zeros
- Cardinal claims to map the RW segment (0xFFFFFFFF41970000-0xFFFFFFFF419BB000)
- g0 address (0xFFFFFFFF4197BF20) is within that range

**Hypothesis:** The high-memory address is NOT actually mapped despite Cardinal's claims. The page table walk might be failing at some level (L0, L1, L2, or L3).

**How to verify:** Use `gdb-check-mappings.gdb` to manually walk the page tables and see where the mapping is missing.

### 2. Relocation Tool Corruption

**Evidence:**
- The relocation tool (tools/relocate-kmazarin.go) modifies ADRP instructions
- It counts 7967 ADRP instructions but the patch code might be wrong
- The relocator claims to handle data pointers but has special code for ADRP

**Hypothesis:** The relocator might be incorrectly modifying the ADRP instruction's immediate field.

**How to verify:**
1. Compare instruction bytes before/after relocation
2. Check if unrelocated kmazarin.elf has different ADRP encoding
3. Temporarily disable ADRP relocation to see if issue persists

### 3. QEMU TCG Bug (Less Likely)

**Evidence:**
- No known bugs found in searches
- QEMU's TCG (Tiny Code Generator) translates ARM64 to host architecture
- Possible edge case with high-memory ADRP execution

**Hypothesis:** QEMU's translator might have a bug with ADRP when PC is in high memory.

**How to verify:**
1. Use `gdb-trace-entry.gdb` to single-step ADRP and see actual x28 value
2. Try different QEMU versions
3. Try with KVM instead of TCG (if on ARM64 host)
4. Test on real hardware if available

### 4. Instruction After ADRP Corrupts x28 (Unlikely)

**Evidence:**
- First syscall (at 0xFFFFFFFF418725C4) succeeds
- This is AFTER rt0_go runs, implying x28 was valid at some point
- Crash happens much later at getHugePageSize

**Hypothesis:** x28 might be set correctly initially but gets corrupted later.

**How to verify:** Set watchpoint on x28 register to see when it changes.

## ⚠️ CRITICAL UPDATE: Root Cause Found!

**GDB debugging revealed the ADRP instruction works perfectly!**

### What GDB Showed:

```
Before ADRP:  x28 = 0x0000000000000000
After ADRP:   x28 = 0xFFFFFFFF4197B000  ✓ CORRECT!
After ADD:    x28 = 0xFFFFFFFF4197BF20  ✓ CORRECT!
Memory at g0: Accessible and zeroed     ✓ CORRECT!

... later ...

At getHugePageSize:  x28 = 0xD65F03C0928004A0  ✗ CORRUPTED!
```

### The Real Bug

**x28 (g register) is being overwritten between `rt0_go` and `getHugePageSize`!**

This is NOT an ADRP bug, relocation bug, or page table bug. Something in the Go runtime initialization code is clobbering the g register.

### Timeline of Events

1. ✅ `rt0_go` sets x28 = 0xFFFFFFFF4197BF20 (correct)
2. ✅ First syscall (`sched_getaffinity`) succeeds (x28 must be valid)
3. ✅ `getCPUCount` runs and stack check succeeds (x28 must be valid)
4. ❌ Between `getCPUCount` and `getHugePageSize`, x28 gets corrupted
5. ❌ `getHugePageSize` stack check fails (x28 = garbage)

### Next Steps

Use `gdb-narrow-corruption.gdb` to find the exact instruction that corrupts x28.

## GDB Debugging Scripts Provided

I've created GDB scripts in `/Users/iansmith/mazzy/debug/`:

### 1. `gdb-adrp-debug.gdb` ✅ SUCCESS
- **Status:** Confirmed ADRP works correctly!
- Breaks at ADRP instruction (0xFFFFFFFF4186EB48)
- Single-steps through ADRP and ADD instructions
- Verifies x28 = 0xFFFFFFFF4197BF20 after initialization
- Continues to getHugePageSize and shows x28 is corrupted

### 2. `gdb-narrow-corruption.gdb` 🔍 USE THIS NEXT
- **Purpose:** Find where x28 gets corrupted
- Sets breakpoints at key functions:
  - After g initialization
  - At first syscall (sched_getaffinity)
  - At getCPUCount entry/exit
  - At getHugePageSize entry
- Checks x28 at each checkpoint
- Will show which function corrupts x28

### 3. `gdb-find-x28-corruption.gdb`
- **Alternative approach:** Single-step through every instruction
- Slower but will catch the exact instruction
- Use if narrow-corruption doesn't pinpoint it

### 4. `gdb-trace-entry.gdb`
- Traces from kmazarin entry point
- Steps through _rt0_arm64_linux -> main -> rt0_go
- Decodes ADRP instruction manually (now proven to work)

### 5. `gdb-check-mappings.gdb` ✅ SUCCESS
- **Status:** Confirmed g0 address is properly mapped!
- Walks TTBR1 page tables (L0 -> L1 -> L2 -> L3)
- Shows page table entries at each level
- Verified 0xFFFFFFFF4197BF20 is accessible

## How to Use the Scripts

See `/Users/iansmith/mazzy/debug/README.md` for detailed instructions.

**Quick start:**
```bash
# Terminal 1 - Start QEMU with GDB server (-S = pause at startup, -s = GDB on port 1234)
~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G \\
    -kernel build/cardinal.elf -nodefaults -device bochs-display -display none \\
    -serial stdio -no-reboot

# Terminal 2 - Run GDB with script
cd /Users/iansmith/mazzy
gdb-multiarch -x debug/gdb-adrp-debug.gdb build/kmazarin.elf
```

## What to Look For in GDB Output

### If ADRP is working:
- x28 = 0xFFFFFFFF4197B000 after ADRP
- x28 = 0xFFFFFFFF4197BF20 after ADD
- Can read valid data from x28 address
- System continues running

### If ADRP is broken:
- x28 = garbage (0xD65F03C0928004A0 or similar)
- Cannot read from x28 address
- GDB shows wrong calculation

### If address not mapped:
- GDB can't read from 0xFFFFFFFF4197BF20
- Page table walk shows missing entry at some level
- TTBR1 page tables incomplete

## Recommended Next Steps

1. **Run `gdb-check-mappings.gdb` first** to verify the address is mapped
   - If not mapped: Fix Cardinal's page table setup
   - If mapped: Proceed to next step

2. **Run `gdb-adrp-debug.gdb`** to see what ADRP actually does
   - If x28 is set incorrectly: Likely QEMU bug or relocation corruption
   - If x28 is set correctly: Look for later corruption

3. **If ADRP sets wrong value:**
   - Run `gdb-trace-entry.gdb` to decode instruction manually
   - Check relocation tool's ADRP patching code
   - Try different QEMU version

4. **If problem persists:**
   - Build unrelocated kmazarin and check if ADRP encoding differs
   - Report bug to QEMU project with minimal test case
   - Implement workaround (Cardinal pre-sets g register)

## References

- [ADRP Stanford Reference](https://www.scs.stanford.edu/~zyedidia/arm64/adrp.html)
- [ADRP & LDR in ARM64 Kernel](https://duetorun.com/blog/20230612/arm64-kernel-adrp-ldr/)
- [AArch64 Loading Addresses](https://devblogs.microsoft.com/oldnewthing/20220809-00/?p=106955)
- [ARM64 Memory Layout](https://www.kernel.org/doc/html/latest/arch/arm64/memory.html)
- [52-bit VA Support](https://opensource.com/article/20/12/52-bit-arm64-kernel)
- [ARM ADR/ADRP Demos](https://duetorun.com/blog/20230609/arm-adr-demo/)
