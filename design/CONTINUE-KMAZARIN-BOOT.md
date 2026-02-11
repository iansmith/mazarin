# Kmazarin x86_64 Boot Crash Investigation - Continuation Prompt

**Date**: 2026-02-11
**Status**: GDT implementation complete and validated; kmazarin crashes at entry
**Branch**: feature/port-x86-64-riscv
**Prior work**: See `design/CONTINUE-X86_64-GDT.md` and `design/GDT-BOOT-CRASH-FINDINGS.md`

## Quick Context

The standard x86_64 GDT implementation (CS=0x08, SS=0x10, Ring 3 at 0x1B/0x23) is **complete and working**. Diplomat successfully:
- Loads kmazarin ELF into memory
- Extracts all 6 required symbols (ExceptionVectorTable, ISRs, syscallEntry)
- Builds kernel page tables (PML4 at 0x7FC01000)
- Configures SYSCALL MSRs and IDT with CS=0x08
- Initializes FS_BASE for Go TLS
- Jumps to kmazarin entry point at 0x43873740

However, **kmazarin immediately triple-faults** after the jump with no output. The system reboots and starts over.

## Current Symptoms

**Serial log output**:
```
VBAR: kmazarin ExceptionVectorTable = 0x438B6F80
IDT CS selector: 0x8
IDT[128]: kmazarin isr128 = 0x438B72C0
SYSCALL entry: 0x438B7C60
Jumping to kmazarin...
[system reboots - UEFI boot sequence starts over]
```

**What we know**:
- No output from kmazarin at all (not even one character)
- Immediate reboot indicates triple fault (CPU exception during exception handler)
- First instruction of `_rt0_amd64` is `mov rdi, [rsp]` (load argc from stack)
- This instruction will page fault if RSP is unmapped or points to invalid memory

## Investigation Plan

### Phase 1: Verify Stack Setup (CRITICAL)

**Hypothesis**: The g0 stack is not mapped in page tables, or RSP points to the wrong address.

**From serial log**:
```
Stacks: g0=0x7D9B3000 exc=0x7D9BB000
Startup env at VA 0xFFFFFFFF43E07D00 (phys 0x7D9B6D00)
```

The startup env is at VA `0xFFFFFFFF43E07D00`, which should be the value passed as `g0StackPtr` to `jumpToKmazarinWithStack`.

**Key questions**:
1. Is `0xFFFFFFFF43E07D00` in the high canonical address range (TTBR1/kernel space)?
2. Is this VA mapped in the PML4 page tables?
3. Does diplomat's `jumpToKmazarinWithStack` correctly set RSP to this address?
4. Is the page actually present (P bit set) in the page tables?

**Files to check**:
- `diplomat/main/uefi_calls_amd64.s:664-665` - Where RSP is set before jump
- `diplomat/main/kernelvm_amd64.go` - Page table setup for g0 stack
- `diplomat/main/startup_env.go:28-32` - Stack address calculation

**Action items**:
```bash
# 1. Check diplomat's jump code
grep -A5 "Switch RSP to g0 stack" diplomat/main/uefi_calls_amd64.s

# 2. Verify stack is mapped in page tables
grep -A10 "g0Pages" diplomat/main/kernelvm_amd64.go

# 3. Check if VA is in correct range
python3 -c "va = 0xFFFFFFFF43E07D00; print(f'VA: 0x{va:X}'); print(f'Canonical high: {va >= 0xFFFF800000000000}')"
```

### Phase 2: Add Debug Breadcrumbs

**Goal**: Confirm kmazarin entry is reached and identify exact crash location.

**Approach 1: Serial output at entry**
Add a breadcrumb as the **absolute first instruction** in kmazarin:

```asm
// In kmazarin's startup assembly (wherever _rt0_amd64_linux is)
TEXT _rt0_amd64_linux(SB), NOSPLIT, $-8
    // CRITICAL: First instruction - output 'K' to serial
    MOVB $'K', AL
    MOVW $0x3F8, DX
    OUTB
    // ... original code
```

If we see 'K' in serial log, kmazarin entry was reached. If not, the jump itself failed.

**Approach 2: Check if kmazarin has custom entry**
Kmazarin might have its own entry point assembly that expects specific setup. Check:
- `kmazarin/kmazarin/abi_stubs_amd64.s` - Any custom _rt0 or entry code?
- `kmazarin/kmazarin/main.go` - Any init requirements?

### Phase 3: Compare with ARM64 Working Boot

ARM64 kmazarin boots successfully. Compare the setup:

**ARM64 (working)**:
- Uses TTBR1 for kernel high memory (0xFFFFFFFF... addresses)
- Sets SP_EL0 to g0 stack before jumping
- VBAR_EL1 set to ExceptionVectorTable before jump
- Stack is pre-mapped in page tables

**x86_64 (crashing)**:
- Uses PML4 for all memory (single address space)
- Sets RSP to g0 stack before jumping
- IDT installed with CS=0x08 before jump
- Stack should be pre-mapped... but is it?

**Compare code**:
```bash
# ARM64 jump code
grep -A20 "jumpToKernelWithEnv" diplomat/main/uefi_calls_arm64.s

# x86_64 jump code
grep -A20 "jumpToKmazarinWithStack" diplomat/main/uefi_calls_amd64.s
```

### Phase 4: Check Page Table State

**Use QEMU monitor** to inspect registers and page tables at crash:

```bash
# Start QEMU with longer timeout
GOTOOLCHAIN=auto QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64 \
  /opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task run-x86_64 TIMEOUT=20

# In another terminal, connect to QEMU monitor
nc 127.0.0.1 4445

# Check registers at crash (run immediately after "Jumping to kmazarin...")
(qemu) info registers
(qemu) info mem
(qemu) x/16xg 0xFFFFFFFF43E07D00  # Dump stack memory (if accessible)
```

**What to look for**:
- RSP value - does it match 0xFFFFFFFF43E07D00?
- CR3 value - should be 0x7FC01000 (PML4 base)
- RIP value - should be 0x43873740 (kmazarin entry) or nearby
- Page tables - is VA 0xFFFFFFFF43E07D00 mapped?

### Phase 5: Verify Kmazarin Relocation

Kmazarin is built at `-T 0x43800000` but loaded at physical address 0x77E00000. The VA range is 0x437FF000-0x43B55F00.

**Check relocation**:
```bash
# Verify entry point address
bin/target-readelf -h build/kmazarin-amd64.elf | grep Entry

# Check if entry point is in mapped VA range
# Entry: 0x43873740, VA range: 0x437FF000-0x43B55F00
# 0x43873740 is within range ✓
```

**Check if code is actually at the expected VA**:
The serial log shows:
```
KernelMap: 0x357 4KB pages at VA 0x437FF000 -> PA 0x77E00000
```

So VA 0x43873740 should map to PA 0x77E00000 + (0x43873740 - 0x437FF000) = 0x77E74740.

Is the code actually there? Check with QEMU:
```
(qemu) x/10i 0x43873740  # Disassemble at kmazarin entry (VA)
```

Should show:
```
0x43873740: e9 fb c8 ff ff    jmp 0x43870040
```

### Phase 6: Check for CR3 Switch Timing

Diplomat might be jumping to kmazarin before switching to the new PML4. Check:

**In `diplomat/main/uefi_calls_amd64.s`**:
- Is CR3 switched to the new PML4 (0x7FC01000) BEFORE jumping?
- Or does kmazarin expect to run with UEFI's page tables initially?

```bash
# Search for CR3 writes in diplomat
grep -n "CR3" diplomat/main/uefi_calls_amd64.s
```

**Expected**: CR3 should be loaded with the new PML4 BEFORE jumping, and the g0 stack VA should be mapped in that PML4.

### Phase 7: Stack Alignment Check

x86_64 ABI requires RSP to be 16-byte aligned before CALL instructions. The entry code does:
```asm
mov rdi, [rsp]      # Load argc
lea rsi, [rsp+8]    # Load argv
sub rsp, 0x28       # Allocate 40 bytes
and rsp, -0x10      # Align to 16 bytes
```

The `and rsp, -0x10` will align the stack, but if the initial RSP is already misaligned, this could cause issues.

**Check**:
```python
# Is startup env address 16-byte aligned?
va = 0xFFFFFFFF43E07D00
print(f"VA: 0x{va:X}")
print(f"Aligned: {(va & 0xF) == 0}")  # Should be True
```

## Debugging Workflow

### Step 1: Quick Verification

```bash
# Set environment
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

# Run with 10s timeout
$GO tool task run-x86_64 TIMEOUT=10

# Check serial log safely
$GO tool safe-serial-read /tmp/diplomat-serial.log | tail -30
```

### Step 2: Add Entry Breadcrumb

The **absolute quickest** way to know if kmazarin entry is reached:

**Option A**: Modify kmazarin's generated assembly (if it exists)
**Option B**: Add serial output in diplomat RIGHT BEFORE the JMP instruction:

```asm
// In diplomat/main/uefi_calls_amd64.s, before line 685 "JMP AX"
MOVB $'J', AL         // 'J' = about to Jump
MOVW $0x3F8, DX
OUTB
JMP AX                // Jump to kmazarin
```

Rebuild diplomat, run, and check serial log:
- If you see 'J' followed by reboot: Jump happened, kmazarin crashed
- If you see 'J' and no reboot: Jump is working, kmazarin is running

### Step 3: Check Stack Mapping

**Read the page table setup code**:
```bash
# Find where g0 stack pages are mapped
grep -B5 -A15 "g0Pages.*KernelG0StackSize" diplomat/main/kernelvm_amd64.go
```

**Look for**:
- Are the pages allocated with `allocatePhysPages()`?
- Are they mapped into the PML4 with `mapPage()` or equivalent?
- What VA range is used (should be 0xFFFFFFFF43E00000-0xFFFFFFFF43E08000)?
- Are the PTEs marked as Present (bit 0 set)?

### Step 4: Add Diagnostic Output

Before jumping, add debug output in diplomat:

```go
// In diplomat/main/main.go, before JumpToKernelWithEnv call
printString("RSP will be: ")
printHex(stackPtr)
printString("\r\n")
printString("Entry: ")
printHex(kernel.Entry)
printString("\r\n")
printString("CR3: ")
// Read CR3 here and print it
printString("\r\n")
```

This will confirm the exact values being passed to the jump function.

## Expected vs Actual State

### Expected State at Jump

| Register | Expected Value | Notes |
|----------|---------------|-------|
| RSP | 0xFFFFFFFF43E07D00 | g0 stack, points to argc |
| RAX | 0x43873740 | kmazarin entry point |
| CR3 | 0x7FC01000 | New PML4 base |
| CS | 0x08 | Ring 0 code (standard GDT) |
| SS | 0x10 | Ring 0 data |
| DS, ES | 0x10 | Ring 0 data |
| FS, GS | 0x00 | Zeroed |
| FS_BASE | (initialTLSBuf+16) | Valid TLS buffer |
| RFLAGS | IF=0 (interrupts disabled) | |
| All other regs | 0 | Cleared for clean state |

### Memory Layout

| Address Range | Content | Mapped? |
|--------------|---------|---------|
| 0x437FF000-0x43B55F00 | kmazarin code/data (VA) | Should be ✓ |
| 0x77E00000-0x77FB8000 | kmazarin code/data (PA) | N/A |
| 0xFFFFFFFF43E00000-0xFFFFFFFF43E08000 | g0 stack (VA) | **CHECK THIS** |
| 0x7D9B3000-0x7D9BB000 | g0 stack (PA) | N/A |

**Critical**: The VA→PA mapping for the g0 stack MUST exist in the PML4 before jumping.

## Files to Examine

**Diplomat jump code**:
- `diplomat/main/uefi_calls_amd64.s:658-685` - jumpToKmazarinWithStack assembly
- `diplomat/main/main.go:400-401` - JumpToKernelWithEnv call
- `diplomat/main/startup_env.go:28-33` - Stack pointer calculation

**Page table setup**:
- `diplomat/main/kernelvm_amd64.go:125-140` - Stack allocation
- `diplomat/main/kernelvm_amd64.go:310-330` - Stack mapping
- `diplomat/main/kernelvm_amd64.go:180-250` - mapPage implementation

**Kmazarin entry**:
- `build/kmazarin-amd64.elf` - Entry point at 0x43873740
- `kmazarin/kmazarin/main.go` - Go main initialization
- `kmazarin/kmazarin/abi_stubs_amd64.s` - Any custom entry code

## Common x86_64 Boot Pitfalls

1. **Forgetting to switch CR3**: Code must run with new page tables active
2. **Stack not mapped**: g0 stack VA must exist in PML4 before use
3. **Wrong address space**: Mixing physical and virtual addresses
4. **Segment selectors**: Using old UEFI selectors instead of new GDT
5. **TLS not initialized**: FS_BASE must point to valid memory (we fixed this)
6. **Interrupts enabled**: Should be disabled (CLI) during early init
7. **Stack misaligned**: RSP must be 16-byte aligned for ABI compliance

## Success Criteria

When the issue is fixed, the serial log should show:
```
Jumping to kmazarin...
[kmazarin output begins - any character indicates success]
```

Even a single character from kmazarin (like a debug 'K') confirms:
- Jump succeeded
- Stack is valid and mapped
- Entry code is executing
- Page tables are working

## ARM64 Reference (Working Example)

For comparison, ARM64 kmazarin boots successfully with this setup:
- Stack at VA 0xFFFFFFFF43E07D00 (same as x86_64)
- TTBR1 page tables (similar to PML4)
- g0 stack pre-mapped before jump
- VBAR set to ExceptionVectorTable before jump

The x86_64 boot should follow the same pattern - all setup done in diplomat before jumping, so kmazarin starts in a fully initialized environment.

## Tools and Commands

```bash
# Build
$GO tool task diplomat --force
$GO tool task kmazarin-amd64 --force

# Run
$GO tool task run-x86_64 TIMEOUT=10

# Debug
$GO tool safe-serial-read /tmp/diplomat-serial.log
echo "info registers" | nc 127.0.0.1 4445
echo "info mem" | nc 127.0.0.1 4445

# Disassemble
objdump -d build/kmazarin-amd64.elf -M intel --start-address=0x43873740 --stop-address=0x43873800

# Check page tables (if accessible via QEMU)
echo "x/16xg 0x7FC01000" | nc 127.0.0.1 4445  # Dump PML4
```

## Conclusion

The GDT implementation is **complete and correct**. The remaining issue is almost certainly a **stack or page table problem**. Start by verifying the g0 stack is mapped in the PML4, then add breadcrumbs to confirm kmazarin entry is reached. This should be solvable quickly once the exact failure point is identified.

Good luck! 🚀
