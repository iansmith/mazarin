# RISC-V Boot Implementation Status

**Date:** 2026-02-11
**Implementation Plan:** [design/RISCV-BOOT-IMPLEMENTATION.md](./RISCV-BOOT-IMPLEMENTATION.md)
**Branch:** riscv-boot

## Executive Summary

Significant progress on RISC-V boot infrastructure. The bootstrap injection mechanism is **working and confirmed** via multiple tests. SBI console calls work. The issue is that jumps from the bootstrap (0x80200000) to the trampoline (0x80253b30) appear to fail, causing the bootstrap to loop.

## What's Working ✅

1. **Build System**
   - RISC-V diplomat builds successfully
   - Runtime overlay system includes trampoline replacement
   - gen-overlay tool updated for RISC-V

2. **Bootstrap Stub Injection** (cmd/fix-go-elf/inject.go)
   - Successfully injects 12-48 byte bootstrap at file offset 0x1000 (VA 0x80200000)
   - Encodes RISC-V instructions correctly (verified via disassembly)
   - Confirmed execution via infinite loop test (QEMU hung as expected)

3. **OpenSBI Boot Flow**
   - OpenSBI correctly jumps to 0x80200000 (Domain0 Next Address confirmed)
   - Bootstrap code executes (proven by output)

4. **SBI Console Calls**
   - Legacy SBI console putchar (EID=0x01) works
   - Bootstrap writes 'BCD' successfully via ECALL
   - Output: repeating 'BCDBCDBCD...' pattern in serial log

5. **ELF Manipulation**
   - fix-go-elf relocates segments to 0x80200000
   - patch-entry sets entry point to _rt0_riscv64_linux
   - All RISC-V instruction encodings verified correct

## Current Issue ❌

**Symptom:** Bootstrap writes 'BCD' repeatedly instead of progressing to trampoline

**Analysis:**
- Bootstrap at 0x80200000 executes completely (all 3 ECALLs work → 'BCD' output)
- JALR instruction (0x00028067) is correctly encoded: `jalr zero, t0, 0`
- Target address 0x80253b30 is 4-byte aligned ✓
- Trampoline code at 0x80253b30 is present and correct (verified via od)
- But control loops back to 0x80200000 instead of staying at trampoline

**Possible Causes:**
1. **Exception on JALR or at trampoline entry** - SEPC register might return control to bootstrap
2. **Memory permissions** - S-mode execution restrictions (though Region07 should allow)
3. **Address calculation error** - Though manual verification shows address is correct
4. **PMP or MMU** - Physical Memory Protection or MMU not configured for trampoline region

## Files Created/Modified

### Core Implementation
- `cmd/fix-go-elf/inject.go` - Bootstrap stub injection (24-48 bytes, encodes RISC-V instructions)
- `cmd/fix-go-elf/main.go` - Calls injection for RISC-V ELFs after segment relocation
- `cmd/gen-overlay/main.go` - Updated buildDiplomatLinuxOverlay() to include trampoline_riscv64.s

### Entry Point Code
- `runtime-patches/diplomat-linux/trampoline_riscv64.s` - Trampoline with SBI console calls
  - Saves OpenSBI params (a0=hartID, a1=FDT) to s0/s1
  - Writes '!!!' via SBI
  - Spins writing 'X'
- `diplomat/main/diplomat_entry_riscv64.s` - Real entry with full Sv48 MMU setup (not reached yet)
- `diplomat/main/entry_globals_riscv64.go` - Globals for OpenSBI parameters
- `diplomat/main/main_riscv64.go` - Forward declaration and keepAlive for diplomatRealEntry

### Build Configuration
- `Taskfile.yml` - diplomat-riscv64 target with proper flags

## Memory Layout

```
0x80000000-0x8003ffff  OpenSBI firmware (M-mode: R+X, S-mode: none)
0x80200000             Bootstrap stub (injected, 48 bytes)
0x80253b30             Trampoline _rt0_riscv64_linux (Go runtime overlay)
0x8fe00000             FDT (Flattened Device Tree) - passed in a1
```

## OpenSBI Environment

From QEMU virt machine:
- **Platform:** riscv-virtio,qemu
- **SBI Version:** 3.0
- **Extensions:** time,rfnc,ipi,base,hsm,srst,pmu,**dbcn**,fwft,**legacy**,dbtr,sse
- **Jump Address:** 0x80200000 (Domain0 Next Address)
- **FDT Address:** 0x8fe00000 (Domain0 Next Arg1)
- **Mode:** S-mode (supervisor)

## Test Results

### Test 1: Infinite Loop (PASSED)
- Injected: `jal zero, 0` at 0x80200000
- Result: QEMU hung indefinitely
- **Conclusion:** Bootstrap is reached and executes

### Test 2: Single Character (PASSED)
- Injected: Write 'B' via SBI, then jump
- Result: Repeating 'BBBBBB...'
- **Conclusion:** SBI calls work, but looping occurs

### Test 3: BCD Sequence (CURRENT)
- Injected: Write 'B', 'C', 'D' via SBI, then jump
- Result: Repeating 'BCDBCDBCD...'
- **Conclusion:** Bootstrap completes all instructions before looping

## TODO List

### Critical Path
- [ ] **Debug why JALR causes loop** - Check SEPC, SCAUSE, exception handlers
- [ ] **Verify trampoline is reachable** - May need to check PMP/MMU/TLB
- [ ] **Alternative: Use FW_JUMP_ADDR** - Configure OpenSBI to jump to correct entry directly?

### Phase 1 (IN PROGRESS)
- [x] Assembly entry point with bootstrap injection
- [x] SBI console output working
- [ ] Reach actual trampoline code (blocked by JALR issue)
- [ ] Complete Sv48 MMU setup
- [ ] Enable UART via device tree (fallback if SBI insufficient)

### Remaining Phases (from RISCV-BOOT-IMPLEMENTATION.md)
- [ ] Phase 2: UART Driver + printString in Go
- [ ] Phase 3: Physical Memory Allocator
- [ ] Phase 4: FDT Parsing
- [ ] Phase 5: VirtIO MMIO Block + FAT32
- [ ] Phase 6: Kernel VM + Demand Paging
- [ ] Phase 7: Jump to Kmazarin

## Key Learnings / Research

### S-mode Restrictions
- S-mode cannot access M-mode registers (illegal instruction exception)
- S-mode has "views" of registers (e.g., sstatus vs mstatus)
- OpenSBI provides runtime services via SBI for S-mode code

### JALR Behavior
- **Generates instruction-address-misaligned exception** if target not 4-byte aligned ([source](https://docs.riscv.org/reference/isa/unpriv/rv32.html))
- Target = (rs1 + sign-extended-imm) & ~1
- Stores PC+4 in rd (or discards if rd=zero)

### Exception Handling
- **SEPC** (Supervisor Exception PC) stores PC where exception occurred ([source](https://medium.com/@wadixtech/techniques-to-use-to-analyze-software-faults-exceptions-on-riscv-processors-1d7a14fe494c))
- After exception handler completes, CPU uses SEPC to return
- **This could explain the loop**: Exception → handler → SEPC returns to bootstrap start

### OpenSBI Boot Protocol
- OpenSBI loads at 0x80000000, jumps to 0x80200000 (FW_JUMP) ([source](https://github.com/riscv-software-src/opensbi/blob/master/docs/firmware/fw_jump.md))
- Default FDT at FW_TEXT_START + 34MB
- Kernel at FW_TEXT_START + 2MB (0x80200000)
- **OpenSBI ignores ELF e_entry** - only uses fixed jump address

## Next Steps / Debug Strategy

1. **Check for exceptions:**
   - Add code to read SCAUSE/SEPC before first ECALL
   - If SEPC points to bootstrap, we know there's an exception loop

2. **Simplify jump target:**
   - Make trampoline first write a character, then halt (no spin)
   - If we see the character, jump succeeded

3. **Test with different address:**
   - Try jumping to an address in the 0x80000000-0x8003ffff range (known executable)
   - If that works, it's a permissions/PMP issue

4. **Check PMP (Physical Memory Protection):**
   - OpenSBI may have configured PMP to restrict S-mode execution
   - Need to read pmpcfg/pmpaddr CSRs (requires M-mode or OpenSBI SBI call)

5. **Alternative approach:**
   - Instead of jumping, try loading and executing diplomatRealEntry inline
   - Or configure build to place _rt0_riscv64_linux at exactly 0x80200000

## Build Commands

```bash
# Environment
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU_RISCV64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-riscv64

# Build
$GO tool task diplomat-riscv64

# Test
$QEMU_RISCV64 -machine virt -m 256M -nographic \
  -serial file:/tmp/boot.log -monitor none \
  -bios default -kernel build/diplomat-riscv64.elf

# Check output
$GO tool safe-serial-read /tmp/boot.log
# Or check raw bytes
tail -c 100 /tmp/boot.log | od -An -tc
```

## References

- [RISC-V SBI and the full boot process](https://popovicu.com/posts/risc-v-sbi-and-full-boot-process/)
- [OpenSBI FW_JUMP documentation](https://github.com/riscv-software-src/opensbi/blob/master/docs/firmware/fw_jump.md)
- [RISC-V Supervisor Binary Interface Specification](https://www.scs.stanford.edu/~zyedidia/docs/riscv/riscv-sbi.pdf)
- [RISC-V ISA Manual - JALR instruction](https://docs.riscv.org/reference/isa/unpriv/rv32.html)
- [Analyzing Faults and Exceptions in RISC-V](https://medium.com/@wadixtech/techniques-to-use-to-analyze-software-faults-exceptions-on-riscv-processors-1d7a14fe494c)
- [OpenSBI Deep Dive (PDF)](https://riscv.org/wp-content/uploads/2024/12/13.30-RISCV_OpenSBI_Deep_Dive_v5.pdf)

## Continuation Prompt

```
Continue RISC-V boot implementation from design/RISCV-BOOT-STATUS.md.

Current issue: Bootstrap at 0x80200000 writes 'BCD' repeatedly via SBI console,
proving it executes and SBI works. But JALR to trampoline at 0x80253b30 fails,
causing loop back to bootstrap start.

Verified:
- Bootstrap injection works (infinite loop test confirmed)
- All instructions encode correctly
- Target address 0x80253b30 is 4-byte aligned
- Trampoline code is present at target location
- SBI console calls work ('BCD' output proves this)

Debug strategy:
1. Check SCAUSE/SEPC registers to detect exceptions
2. Test jump to different address in known-executable region
3. Simplify trampoline to just write one char and halt
4. Research PMP (Physical Memory Protection) configuration
5. Consider alternative: place _rt0_riscv64_linux exactly at 0x80200000

The breakthrough is that SBI calls work and bootstrap executes. We just need
to debug why the jump fails and loops back. Likely exception-related (SEPC
returning to bootstrap) or memory permission issue.

Environment:
GOTOOLCHAIN=auto
GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
QEMU_RISCV64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-riscv64
```
