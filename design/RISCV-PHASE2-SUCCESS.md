# RISC-V Phase 2 - SUCCESS!

## Date: 2026-02-12

## Summary

**RISC-V diplomat boot chain is now working!** All Phase 2 objectives achieved:

✅ OpenSBI boots diplomat via `-kernel` flag
✅ Bootstrap stub successfully jumps to entry point (AUIPC+JALR)
✅ Trampoline initializes g0/m0/TLS correctly
✅ DiplomatEntry executes (Go runtime initialized)
✅ Memory span tracking working
✅ Serial UART output functioning

## Output

```
D1234567TCJEWDiplomat UEFI Bootloader
DBG: before InitializeSpans
DBG: spans OK
GRISC-V boot: UEFI services not available
RISC-V boot: Need to implement VirtIO block device access

=== RISC-V Diplomat Phase 2 Complete ===
OpenSBI boot:     SUCCESS
Bootstrap stub:   SUCCESS
Trampoline init:  SUCCESS
g0/m0/TLS setup:  SUCCESS
DiplomatEntry:    SUCCESS
Memory spans:     SUCCESS
```

## Key Fixes Applied

### 1. Linker Address Flag (`-T 0x80200000`)
- **File**: `Taskfile.yml:748`
- **Change**: Added `-T 0x80200000` to diplomat-riscv64 ldflags
- **Why**: Without this, Go linker creates segments at wrong addresses
- **Result**: Segments load at correct address after OpenSBI (0x80200000)

### 2. ELF Post-Processing (fix-go-elf)
- **File**: `cmd/fix-go-elf/main.go:91-130`
- **Change**: Detect negative p_offset from `-T` flag, skip RISC-V relocation
- **Why**: Double-processing broke PC-relative AUIPC references
- **Result**: Binary works correctly without relocation

### 3. Bootstrap Stub (AUIPC+JALR)
- **File**: `cmd/fix-go-elf/inject.go:40-102`
- **Change**: Implemented AUIPC+JALR to jump from segment start to entry point
- **Why**: OpenSBI jumps to segment start (0x80200000), not ELF entry (0x80253DE0)
- **Result**: Bootstrap stub successfully redirects execution

### 4. S-mode CSR Usage
- **File**: `runtime-patches/diplomat-linux/trampoline_riscv64.s:31-33`
- **Change**: Changed M-mode CSRs (mtvec, mcause, mepc, mtval) to S-mode (stvec, scause, sepc, stval)
- **Why**: OpenSBI runs diplomat in S-mode, not M-mode
- **Result**: Trap handler works correctly

### 5. RISC-V Boot Sequence
- **Files**: `diplomat/main/boot_riscv64.go`, `diplomat/main/platform_riscv64.go`
- **Change**: Created RISC-V-specific GetBootDeviceRISCV function
- **Why**: UEFI boot services don't exist in OpenSBI environment
- **Result**: Clean detection and messaging of non-UEFI environment

## Technical Details

### Memory Layout
```
0x80000000 - 0x80200000  OpenSBI firmware (reserved)
0x80200000 - 0x802xxxxx  Diplomat code/data (loaded via -kernel)
0x81218000               Stack top (32KB, grows down)
0xFFE00000               FDT (Flattened Device Tree) from OpenSBI
```

### Register State at Entry
```
A0 (X10): Hart ID = 0
A1 (X11): FDT pointer = 0xFFE00000
SP (X2):  Set to 0x81218000 by trampoline
g  (X27): Points to runtime.g0
TP (X4):  Points to TLS (tlsBlock + 8)
```

### Boot Sequence
1. QEMU loads OpenSBI firmware + diplomat ELF
2. OpenSBI initializes M-mode, drops to S-mode
3. OpenSBI jumps to 0x80200000 (segment start)
4. Bootstrap stub (AUIPC+JALR) jumps to 0x80253DE0 (_rt0_riscv64_linux)
5. Trampoline sets up g0/m0/TLS, installs trap handler
6. Jump to diplomatEntryWrapper (assembly debug wrapper)
7. Call DiplomatEntry (Go function)
8. Initialize memory spans
9. Attempt to get block device → detect non-UEFI → print Phase 2 success

## Files Modified

### Core Boot Files
- `Taskfile.yml` - Added `-T 0x80200000` linker flag
- `cmd/fix-go-elf/main.go` - Added negative offset detection
- `cmd/fix-go-elf/inject.go` - Implemented AUIPC+JALR bootstrap stub
- `runtime-patches/diplomat-linux/trampoline_riscv64.s` - Fixed S-mode CSRs

### New Files
- `diplomat/main/boot_riscv64.go` - RISC-V boot sequence (non-UEFI)
- `diplomat/main/uart_riscv64.s` - Direct UART driver
- `diplomat/main/uart_riscv64.go` - UART Go wrappers
- `diplomat/main/uart_direct_riscv64.s` - Minimal UART
- `diplomat/main/diplomat_entry_wrapper_riscv64.s` - Debug wrapper

### Updated Files
- `diplomat/main/platform_riscv64.go` - Use GetBootDeviceRISCV instead of UEFI
- `diplomat/main/main.go` - printString handles nil systemTable
- `diplomat/main/tls_riscv64.s` - debugPortOut via UART

## Next Steps: Phase 3

### Phase 3a: FDT Parsing
1. **Save FDT pointer**: Modify trampoline to store A1 to `fdtPointer` global
2. **Parse FDT header**: Verify magic (0xD00DFEED), find structure/strings
3. **Parse `/memory`**: Extract RAM base + size (replaces UEFI GetMemoryMap)
4. **Parse `/reserved-memory`**: Find OpenSBI reserved regions
5. **Parse `/cpus`**: Count harts, extract ISA string (Svpbmt, Zicbom)
6. **Parse `/chosen`**: Get bootargs, stdout-path

**Reference**: See `design/RISCV-diplomat-needed.md` Part A, section A5

### Phase 3b: Memory Allocation
1. **Implement bump allocator**: Advance pointer through usable RAM
2. **Exclude diplomat binary**: Calculate end address, start after it
3. **Exclude OpenSBI**: Use FDT `/reserved-memory` to skip firmware regions
4. **Replace UEFIAllocatePages**: All allocation calls use bump allocator

**Reference**: See `design/RISCV-diplomat-needed.md` Part A, section A2

### Phase 3c: VirtIO Block Device
1. **Scan PCI bus**: ECAM at 0x30000000, find VirtIO block devices
2. **Initialize VirtIO**: Set up virtqueues, negotiate features
3. **Implement BlockDevice**: Read/Write methods for FAT32
4. **Replace GetBootDeviceRISCV**: Return real VirtIO block device

**Reference**: Existing code in `kmazarin/device/virtio/block/`

### Phase 3d: Kernel Loading
1. **Mount FAT32**: Use existing `fat32.MountWith(dev, diplomatAllocator)`
2. **Load kmazarin.elf**: Read from `/KMAZARIN.ELF` on disk
3. **Parse ELF**: Extract segments, entry point
4. **Copy to memory**: Allocate pages, copy ELF segments

This is the same as ARM64/x86_64, just using VirtIO instead of UEFI.

### Phase 3e: Kernel VM Setup
1. **Build Sv48 page tables**: 4-level, same logic as ARM64/x86_64
2. **Map kernel code**: High VA (0xFFFFFFFF43800000+)
3. **Linear map**: All RAM at 0xFFFFFFFF00000000+
4. **Map stacks**: g0 stack, exception stack
5. **Install demand paging handler**: STVEC points to page fault handler
6. **Build auxv**: Same auxv entries as other platforms
7. **Jump to kmazarin**: With proper stack, STVEC, auxv

**Reference**: See `design/RISCV-diplomat-needed.md` Part B and C

## Testing Commands

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_RISCV64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-riscv64

# Build
$GO tool task diplomat-riscv64

# Run (5 second timeout)
$GO tool task run-diplomat-riscv64 TIMEOUT=5

# View output
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

## References

- **Root Cause Analysis**: `design/RISCV-PHASE2-BLOCKER.md`
- **Implementation Plan**: `design/RISCV-diplomat-needed.md`
- **Boot Status**: `design/RISCV-BOOT-STATUS.md`
- **Memory Notes**: `memory/riscv-boot-debug.md`

## Conclusion

**Phase 2 is complete!** The RISC-V diplomat boot chain works end-to-end from OpenSBI to DiplomatEntry. The foundation is solid and ready for Phase 3: FDT parsing, memory management, VirtIO block devices, and kmazarin loading.

This represents a major milestone: RISC-V is now a viable third architecture for the Mazzy project, joining ARM64 and x86_64.
