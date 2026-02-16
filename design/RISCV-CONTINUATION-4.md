# RISC-V Continuation Prompt #4 — Userspace ELF Loading

## Context

RISC-V kmazarin is now booting successfully with framebuffer, block device, and DTB parsing
all working. Three major bugs were fixed in this session. The next task is getting userspace
ELF loading to work.

## Bugs Fixed This Session

### 1. FDT Overwritten by Framebuffer (FIXED)
OpenSBI FDT at PA 0xFFE00000 is within the buddy allocator's range (0x9434C000-0x100000000).
The GPU framebuffer allocated at PA 0xFF000000 (~16MB, 0xFF000000-0xFFFD2000) overlaps the FDT.

**Fix**: Added `copyFDTToSafeLocation()` in `kmazarin/kmazarin/main.go`. Copies 6084-byte FDT
to a buddy-allocated buffer before GPU init. Uses safe copy for DTB parsing.

### 2. SATP Value Construction (FIXED)
`SwitchTTBR0WithASID()` constructed ARM64-format value: `(asid << 48) | PA`. On RISC-V, SATP
format is `[63:60]=MODE(9=Sv48) | [59:44]=ASID | [43:0]=PPN(PA>>12)`. Writing MODE=0 disabled
address translation.

**Fix**: Extracted `constructTTBR0Value(l0PA, asid)` as arch-specific function in each
`paging_{arch}.go`. `SwitchTTBR0WithASID` now uses this constructor.

### 3. Process Page Table Missing Kernel Code (FIXED)
`initProcessL0()` on RISC-V only copied L3[256-511] (upper half). But kmazarin code at
VA 0x43800000 is in L3[0], L2[1]. Switching SATP lost kernel code access.

**Fix**: For L3[0], allocate new L2 per process, copy kernel's L2[1]+ (kmazarin code),
leave L2[0] empty for user space. Same pattern as x86_64's `initProcessL0`.

## Current State — Load Access Fault

After all fixes, SATP switch works, kernel continues executing. But during `SyscallLaunch`,
a load access fault occurs:

```
!PF@00000000438971CC/5[FFFFFFFF00094BED>000000004389711C<SP=FFFFFFC14805CC08A1=00000000439FF420N=00000000000943F5G=FFFFFFC1480041C0C=0000000025080801/00007FFE00000000
```

- `PF@00000000438971CC` — fault VA (kmazarin code range 0x43800000+)
- `/5` — RISC-V exception cause 5 = **load access fault** (NOT page fault!)
- `>000000004389711C` — SEPC (exception PC)
- `SP=FFFFFFC14805CC08` — kernel stack (high VA, correct)
- `G=FFFFFFC1480041C0` — g register (high VA, correct)

Cause 5 means the page walk found a valid PTE, but the physical access was denied.
This is different from cause 13 (page fault = invalid PTE). Possible issues:
1. PTE permissions missing R bit in the new process page table L2 copy
2. Physical address in PTE is wrong (shouldn't be — it's copied from kernel)
3. PMA violation
4. Something in `AllocAndMapUserPageWithL0` or `MapPAToKernelScratch` going wrong

## What to Investigate Next

1. **Identify the faulting function**: `bin/target-addr2line -e build/kmazarin-riscv64.elf 0x4389711C`

2. **Check the exception handler output format**: Read `kmazarin/kmazarin/exceptions_riscv64.s`
   to understand the trap handler debug output format

3. **Verify process page table correctness**: After SATP switch, the kmazarin code pages
   should still be readable. Add debug output to dump the PTE for the faulting VA.

4. **Check if the fault is during ELF loading**: The `SyscallLaunch` flow calls:
   - `CreateProcessPageTable()` → `initProcessL0()` (creates process L3 with kernel mappings)
   - `SwitchTTBR0WithASID(processL0PA, 0)` → writes SATP (kernel code should still work!)
   - `MapUserFramebuffer()` → maps framebuffer into user page table
   - `loadELF()` → parses ELF, calls `AllocAndMapUserPageWithL0` + `MapPAToKernelScratch`

5. **Key RISC-V difference**: On RISC-V, there is NO TTBR0/TTBR1 split. When SATP changes,
   ALL address translations change. The process page table must contain ALL kernel mappings
   (L3[0] for kmazarin code, L3[256-511] for linear map/heap/scratch).

## Files Modified This Session

| File | Change |
|------|--------|
| `kmazarin/kmazarin/main.go` | `copyFDTToSafeLocation()`, `safeDTBVirtAddr`; use safe copy in `testDeviceDiscovery` |
| `kmazarin/kmem/paging.go` | `SwitchTTBR0WithASID` uses arch-specific `constructTTBR0Value` |
| `kmazarin/kmem/paging_riscv64.go` | `constructTTBR0Value` (Sv48 SATP); `initProcessL0` L3[0] split |
| `kmazarin/kmem/paging_arm64.go` | `constructTTBR0Value` (ARM64 TTBR0 format) |
| `kmazarin/kmem/paging_amd64.go` | `constructTTBR0Value` (x86_64 CR3 format) |

## Previously Fixed (Still Relevant)

| File | Change |
|------|--------|
| `kmazarin/kmem/buddy.go` | MaxOrder 12→13 (for 16MB framebuffer alloc) |
| `kmazarin/kmem/unified_pool.go` | `AllocContiguousPages` delegates to buddy when initialized |
| `kmazarin/device/virtio/gpu/gpu.go` | Dynamic framebuffer PA allocation via buddy |
| `Taskfile.yml` | disk-riscv64 (full disk with userspace), asm_linux_riscv64.s |

## Build & Run

```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task kmazarin-riscv64 && $GO tool task run-diplomat-riscv64 TIMEOUT=30
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

## Working on RISC-V

- VirtIO GPU framebuffer (1920x1080, all GPU commands succeed, display visible)
- VirtIO MMIO block device (FAT32 reads, sector 0 sig=0xAA55)
- DTB parsing (6084 bytes FDT from OpenSBI, PLIC/CLINT/UART/VirtIO discovered)
- Timer interrupts and context switches
- SATP switching (process page table with kernel mappings preserved)
- ARM64 verified still working after all changes
