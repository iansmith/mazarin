# RISC-V Boot - Phase 2 Continuation Prompt

## Current State (2026-02-11)

### ✅ Completed - Phase 1: Assembly Entry + UART + Sv48 MMU
- Bootstrap JAL at 0x80000000 successfully jumps to trampoline at 0x80053b30
- Trampoline (`_rt0_riscv64_linux` in `runtime-patches/diplomat-linux/trampoline_riscv64.s`) initializes Go runtime
- Minimal g0/m0/TLS setup complete
- **DiplomatEntry successfully called!**
- Output: "D1234567TC" (10 bytes) proving full initialization sequence works

### 🔄 In Progress - Phase 2: UART Driver + printString in Go

**Current Blocker**: DiplomatEntry is called and executing but produces no output.

**What we know**:
- Assembly trampoline writes markers 'D', '1-7', 'T', 'C' to UART successfully
- After 'C', control transfers to `main.DiplomatEntry` (Go code)
- DiplomatEntry runs but no further UART output appears
- This suggests DiplomatEntry is running but either:
  1. Crashes/hangs before reaching any UART writes
  2. Go's UART driver isn't working
  3. printString() isn't working in RISC-V environment

## Phase 2 Goals

1. **Get DiplomatEntry producing output**
   - Verify DiplomatEntry can print to UART
   - Confirm printString() works
   - See diplomat's "Diplomat UEFI Bootloader" banner

2. **Debug why no output appears**
   - Add debug markers in DiplomatEntry to isolate the issue
   - Check if it's crashing during initialization
   - Verify UART driver works from Go code

3. **Complete basic diplomat initialization**
   - Get past DiplomatEntry's early setup
   - Reach the "Diplomat UEFI Bootloader" message
   - Verify debugPortOut() works

## Immediate Next Steps

### Step 1: Investigate DiplomatEntry
Read `diplomat/main/main.go` and find the `DiplomatEntry` function. Add UART debug markers at the very beginning to see if it's executing at all.

**Action**: Add inline assembly UART writes to DiplomatEntry (using WORD directives) at strategic points to trace execution.

### Step 2: Check UART Driver
The RISC-V UART driver might not exist or might be broken. Check for:
- `diplomat/main/uart_riscv64.go` or similar
- UART initialization in DiplomatEntry
- printString() implementation for RISC-V

**Action**: If UART driver doesn't exist, create minimal version that writes to 0x10000000 (NS16550A UART).

### Step 3: Verify printString Works
Once we can write from Go code, verify printString() works:
- Call printString("RISC-V Boot\r\n") early in DiplomatEntry
- Should see output after the "D1234567TC" markers

### Step 4: Complete Phase 2
Once printString works:
- Let DiplomatEntry run through initialization
- See diplomat's boot messages
- Confirm debugPortOut() works
- Mark Phase 2 complete

## Technical Context

### Memory Layout
- Code loaded at: 0x80000000 (firmware, -bios none)
- Stack: 0x81218000 (32KB)
- Page tables: 0x81200000+ (for future MMU setup)

### Register Usage
- **X5**: UART base (0x10000000) - **DO NOT OVERWRITE**
- **X6**: Temp for UART writes
- **X27** (g): Points to runtime.g0
- **X4** (TP): Thread pointer for TLS
- **X28-X30**: Safe temps (don't conflict with UART)

### UART (NS16550A at 0x10000000)
```asm
// Write character in X6 to UART
LUI  X5, 0x10000      // WORD $0x100002b7
ADDI X6, X0, <char>   // WORD $0x0<char>00313
SB   X6, 0(X5)        // WORD $0x00628023
```

### Files Modified This Session
- `runtime-patches/diplomat-linux/trampoline_riscv64.s` - Trampoline with g0/m0 init
- `diplomat/main/main_riscv64.go` - Forward declarations, boot globals
- `cmd/fix-go-elf/main.go` - Fixed p_offset accounting (dynamic tracking)

## Remaining Phases (After Phase 2)

### Phase 3: Physical Memory Allocator
- Parse FDT (Flattened Device Tree) from A1 register
- Discover RAM regions
- Implement bump allocator for early boot
- Set up memory spans for mmap

### Phase 4: FDT Parsing
- Full FDT parsing implementation
- Device discovery (UART, VirtIO devices)
- Memory region enumeration

### Phase 5: VirtIO MMIO Block + FAT32
- VirtIO block driver (shared with x86_64/ARM64)
- Mount FAT32 filesystem
- Read KMAZARIN.ELF from disk

### Phase 6: Kernel VM + Demand Paging
- Build Sv48 page tables for kmazarin
- Set up linear map (physical → virtual)
- Enable MMU (SATP register)
- Install page fault handler

### Phase 7: Jump to Kmazarin
- Load kmazarin ELF into memory
- Set up kmazarin's exception vector (VBAR equivalent)
- Build auxv entries on stack
- Jump to kmazarin entry point

## Important Notes

### Don't Use rt0_go!
We skip `runtime.rt0_go` entirely because it expects a Linux environment (argc/argv). Instead, we do minimal g0/m0/TLS setup and jump directly to DiplomatEntry.

### Register Conflicts
Watch for conflicts with X5 (UART). Always use X28-X30 for temps in assembly that needs to preserve UART access.

### Serial Log Safety
**CRITICAL**: Never read `/tmp/diplomat-serial.log` directly! Always use:
```bash
$GO tool safe-serial-read /tmp/diplomat-serial.log
```

### Build Commands
```bash
# Set environment
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_RISCV64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-riscv64

# Build and run
$GO tool task diplomat-riscv64
$GO tool task run-diplomat-riscv64 TIMEOUT=5

# Check output
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

## Success Criteria for Phase 2

- [ ] DiplomatEntry executes and produces output
- [ ] See "Diplomat UEFI Bootloader" or similar message
- [ ] printString() works from Go code
- [ ] debugPortOut() works
- [ ] Can print debug messages during initialization

Once these are achieved, Phase 2 is complete and we move to Phase 3 (Physical Memory Allocator).

## All Pending Tasks

From task list:
- #2: Phase 2: UART Driver + printString in Go (IN PROGRESS - current focus)
- #3: Phase 3: Physical Memory Allocator
- #4: Phase 4: FDT Parsing
- #5: Phase 5: VirtIO MMIO Block + FAT32
- #6: Phase 6: Kernel VM + Demand Paging
- #7: Phase 7: Jump to Kmazarin
- #8: Investigate: Try SBI console calls instead of direct UART (optional)

## Start Here

When resuming this work:

1. Read this document completely
2. Review the current output: "D1234567TC"
3. Start with Step 1: Add debug markers to DiplomatEntry
4. Work through the immediate next steps above
5. Focus on getting **any** output from Go code
6. Once printString works, Phase 2 is essentially done!

The hardest part (runtime initialization) is complete. Now we just need to get diplomat's Go code producing output!
