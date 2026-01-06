# GDB Debugging Guide for Mazzy

## Issue Found: Stale Binary in Flash Directory

**CRITICAL**: The `make clean` target was NOT cleaning the `flash/` directory, so `tools/run/run-mazboot-text` was loading stale binaries even after rebuilds!

**Fix Applied**: Updated Makefile to clean `flash/*` in addition to `build/*`.

**Before running any tests, do a complete clean build:**
```bash
make clean
make all
```

This will now clean both `build/mazboot/*` AND `flash/*`, ensuring you run the latest code.

---

## GDB Debug Scripts

Three GDB scripts have been created to debug the memory allocation failure:

### 1. `debug_page_fault.gdb` - Trace Demand Paging

Monitors page faults and mmap syscalls to verify demand paging works correctly.

**Usage:**
```bash
# Terminal 1: Start QEMU with GDB server (waits for debugger)
NOGRAPHIC=1 timeout 30 /opt/homebrew/bin/qemu-system-aarch64 \
  -M virt,virtualization=off \
  -cpu cortex-a72 \
  -m 1G \
  -kernel flash/mazboot.elf \
  -serial mon:stdio \
  -semihosting \
  -s -S

# Terminal 2: Run GDB with script
~/mazzy/bin/target-gdb -x debug_page_fault.gdb build/mazboot/mazboot.elf
```

**What it traces:**
- `SyscallMmap` entry with all parameters (addr, len, prot, flags, fd, offset)
- `SyscallMmap` return value (distinguishes errors from success)
- `HandlePageFault` calls with fault address and status
- Page mapping success/failure

**Expected output:**
```
=== SyscallMmap called ===
addr:  0x0000000000000000
len:   0x0000000000010000
prot:  0x3
flags: 0x11
fd:    -1
offset: 0x0

SyscallMmap returning: 0x0000000048000000
  -> SUCCESS: address = 0x0000000048000000

=== HandlePageFault called ===
Fault Address: 0x48000000
Fault Status:  0x92000007
Page mapped successfully, returning true
```

---

### 2. `debug_register_corruption.gdb` - Trace Syscall Return Path

Traces register values through the entire syscall path to find where corruption happens.

**Usage:**
```bash
# Same as above - Terminal 1 runs QEMU with -s -S
# Terminal 2:
~/mazzy/bin/target-gdb -x debug_register_corruption.gdb build/mazboot/mazboot.elf
```

**What it traces:**
- All register values at `SyscallMmap` entry (X0-X5, LR, SP)
- Register state at `syscall_return` before ERET (X0-X5, ELR_EL1, SP_EL0, SP_EL1)
- Single-steps through ERET instruction
- Register state after ERET (back in kmazarin)

**IMPORTANT**: You need to find the actual address of `syscall_return` first:
```bash
~/mazzy/bin/target-objdump -d build/mazboot/mazboot.elf | grep syscall_return
```

Then edit `debug_register_corruption.gdb` and replace the placeholder:
```gdb
# Line 23 - replace with actual address
break *0x401XXXXX  # syscall_return address
```

**Expected behavior:**
- X0 should contain mmap return value through entire path
- If X0 changes unexpectedly, that's where corruption occurs

---

### 3. `debug_walk_page_tables.gdb` - Verify Page Table Mapping

Manually walks ARM64 4-level page tables to verify that demand paging actually mapped the page.

**Usage:**
```bash
# Same as above - Terminal 1 runs QEMU with -s -S
# Terminal 2:
~/mazzy/bin/target-gdb -x debug_walk_page_tables.gdb build/mazboot/mazboot.elf
```

**What it does:**
- Breaks when `SyscallMmap` returns 0x48000000
- Reads TTBR0_EL1 to get L0 table base
- Walks L0 → L1 → L2 → L3 tables for VA 0x48000000
- Shows each table entry with:
  - Valid/Invalid status
  - Table vs Block/Page descriptor
  - Physical address
  - Memory attributes (Device vs Normal Cacheable)
  - Access permissions (RW EL1 only, etc.)
  - Execute Never flag

**Expected output if page is mapped:**
```
=== Walking page tables for VA 0x0000000048000000 ===

TTBR0_EL1: 0x0000000041000000

L0 Table Base: 0x0000000041000000
L0 Index: 0 (bits 47:39 of VA)
L0 Entry Address: 0x0000000041000000
  PTE: 0x0000000041001003
    VALID
    TABLE descriptor (points to next level)
    Physical Address: 0x0000000041001000

L1 Table Base: 0x0000000041001000
L1 Index: 0 (bits 38:30 of VA)
L1 Entry Address: 0x0000000041001000
  PTE: 0x0000000041002003
    VALID
    TABLE descriptor (points to next level)
    Physical Address: 0x0000000041002000

L2 Table Base: 0x0000000041002000
L2 Index: 2 (bits 29:21 of VA)
L2 Entry Address: 0x0000000041002010
  PTE: 0x0000000041003003
    VALID
    TABLE descriptor (points to next level)
    Physical Address: 0x0000000041003000

L3 Table Base: 0x0000000041003000
L3 Index: 0 (bits 20:12 of VA)
L3 Entry Address: 0x0000000041003000
  PTE: 0x0000000048000707
    VALID
    BLOCK/PAGE descriptor (final mapping)
    Physical Address: 0x0000000048000000
    AttrIndx: 1 (Normal Cacheable)
    AP: 0 (RW EL1 only)
    XN: Execute Never

*** Final Physical Address: 0x0000000048000000 ***
```

**If page is NOT mapped:**
```
L3 Entry Address: 0x0000000041003000
  PTE: 0x0000000000000000
    INVALID (bit 0 not set)

*** L3 entry invalid - Page not mapped! ***
```

**Manual usage:**
You can also use the `walk_tables` command manually in GDB:
```gdb
# After connecting to QEMU
(gdb) walk_tables 0x48000000
(gdb) walk_tables 0x48001000
(gdb) walk_tables 0x5F000000  # Check g0 stack
```

---

## Debugging Workflow

### Step 1: Verify Clean Build
```bash
make clean
make all
```

### Step 2: Check for Stale Output
If you still see "SVC=00000000" or other old debug output, the issue was the stale flash binary. The clean build should fix this.

### Step 3: Test with Page Fault Script
```bash
# Terminal 1
NOGRAPHIC=1 timeout 30 /opt/homebrew/bin/qemu-system-aarch64 \
  -M virt,virtualization=off -cpu cortex-a72 -m 1G \
  -kernel flash/mazboot.elf -serial mon:stdio -semihosting -s -S

# Terminal 2
~/mazzy/bin/target-gdb -x debug_page_fault.gdb build/mazboot/mazboot.elf
```

Look for:
- Does mmap return 0x48000000? (SUCCESS)
- Does HandlePageFault get called when kmazarin accesses 0x48000000?
- Does page mapping succeed?

### Step 4: Walk Page Tables
```bash
# Same QEMU setup, different script
~/mazzy/bin/target-gdb -x debug_walk_page_tables.gdb build/mazboot/mazboot.elf
```

Look for:
- Is L3 entry valid after mmap?
- Does it point to PA 0x48000000?
- Are attributes correct? (Normal Cacheable, RW, XN)

### Step 5: Trace Register Corruption (if needed)
If mmap succeeds but allocation still fails, trace the syscall return path:
```bash
# First find syscall_return address
~/mazzy/bin/target-objdump -d build/mazboot/mazboot.elf | grep syscall_return

# Edit debug_register_corruption.gdb with actual address
# Then run:
~/mazzy/bin/target-gdb -x debug_register_corruption.gdb build/mazboot/mazboot.elf
```

Look for:
- Does X0 stay 0x48000000 through entire path?
- Does ERET corrupt X0?
- Does kmazarin's conversion code change X0?

---

## Interpreting Results

### If mmap succeeds but page fault never happens:
- Kmazarin might not be accessing the memory
- Check kmazarin's `persistentalloc1()` logic
- Verify that `sysAlloc()` actually writes to the returned address

### If page fault happens but mapping fails:
- Check `HandlePageFault()` error messages
- Verify span registration covers 0x48000000-0xC8000000
- Check if physical frame allocator has free pages

### If page is mapped but allocation still fails:
- Register corruption in syscall return path
- Kmazarin's `sysMmap` conversion logic might be wrong
- Check if io_setup syscall corrupts state

### If you still see "SVC=00000000":
- Flash directory wasn't cleaned properly
- Try manual clean: `rm -rf flash/* build/mazboot/*`
- Rebuild: `make all`

---

## Common QEMU Flags

**Minimal run** (no debugging):
```bash
NOGRAPHIC=1 timeout 30 bin/run-mazboot
```

**With GDB server** (wait for debugger):
```bash
NOGRAPHIC=1 timeout 30 /opt/homebrew/bin/qemu-system-aarch64 \
  -M virt,virtualization=off -cpu cortex-a72 -m 1G \
  -kernel flash/mazboot.elf -serial mon:stdio -semihosting \
  -s -S
```

**With execution trace** (very verbose):
```bash
NOGRAPHIC=1 /opt/homebrew/bin/qemu-system-aarch64 \
  -M virt,virtualization=off -cpu cortex-a72 -m 1G \
  -kernel flash/mazboot.elf -serial mon:stdio -semihosting \
  -d exec,int,cpu_reset -D qemu.log
```

---

## Next Steps

1. **Do clean rebuild** with fixed Makefile
2. **Run page fault script** to see if demand paging works
3. **Walk page tables** to verify mapping
4. **If still failing**, trace registers for corruption

The stale flash binary was likely the root cause of seeing old debug output. A clean rebuild should show the actual current behavior.
