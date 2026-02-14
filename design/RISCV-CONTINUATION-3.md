# RISC-V Kmazarin Boot Debugging — Continuation Prompt #3

## Instruction for Claude

Please continue the conversation from where we left off without asking the user any further questions. Continue with the last task that you were asked to work on.

## What Was Being Worked On

Debugging RISC-V kmazarin bare-metal Go kernel boot on QEMU virt. The kernel now boots through the ENTIRE initialization sequence and reaches the idle loop. The remaining issue is an instruction page fault during the idle loop's context switch.

## Current Boot Progress (WORKING)

The following all work successfully:
1. OpenSBI → Diplomat boots, loads kmazarin ELF
2. Kmazarin Go runtime initializes (rt0_go → schedinit → mallocinit)
3. Two clone threads created (sysmon + templateThread)
4. runtime.main goroutine starts
5. "[Main] Kmazarin kernel starting..." and "[Main] Runtime ready"
6. VirtIO GPU found via PCI, CREATE_2D succeeds
7. VirtIO Block PCI scan (no device found — expected, RISC-V uses MMIO transport)
8. Device discovery, initCachedIC, initVirtIOInputDevices
9. EnableIRQs, EnableTimerIRQ
10. Tries to launch /dapope.elf and /stdio.elf (fails — no block device)
11. "Second EnableIRQs done"
12. Enters KernelIdleLoop → **CRASHES** with instruction page fault

## The Bug: Instruction Page Fault at 0x438AE498

### What happens
```
PX[00000000438AE498]@00000000438AE498>00000000438AE498
```
- `P` = page fault entry, `X` = instruction page fault (scause=12)
- stval = sepc = RA = 0x438AE498 (all the same)
- Address 0x438AE498 is in `main.KernelIdleLoop` at line `threads.go:821`
- Disassembly: `0x438ae498  f8dff06f  JMP -29(PC)` — a JMP back to loop start after `YieldToReadyThread`

### Why it's puzzling
- Diplomat pre-maps ALL kernel code pages (0x43800000-0x43B79000, 0x379 pages) with PTE_LEAF_RWX (V|R|W|X|A|D = 0xCF)
- 0x438AE498 is at page 0x438AE000, well within the mapped range
- These pages work fine during the entire boot sequence (many functions execute from this range)
- The fault happens only AFTER a context switch in the idle loop

### Likely root cause hypothesis
The instruction page fault occurs when `load_context_and_sret` in `exceptions_riscv64.s` (line 868) does `sfence.vma` (full TLB flush), then SRET tries to fetch from SEPC=0x438AE498. The TLB miss triggers a page table walk that fails.

Possible reasons the page table walk fails:
1. **Demand pager corruption**: The Go demand pager (`HandlePageFaultAsm` in `kmem/paging.go`) might be creating new page table entries that overwrite intermediate entries (L3/L2/L1) in the code mapping path: L3[0] → L2[1] → L1[0x1C5] → L0[0xAE]
2. **Page table page reuse**: A physical page used by diplomat's page table might be allocated by the unified pool bump allocator for data pages, corrupting the page table
3. **Architecture-specific paging code**: The RISC-V paging code in `paging_riscv64.go` might have a bug that creates entries in the wrong tree

### Investigation needed
- Read `kmazarin/kmem/paging.go` (the HandlePageFault function, ~lines 400-600) and `kmazarin/kmem/paging_riscv64.go` to understand how demand paging works on RISC-V
- Check if the demand pager's L3[0] handling could clobber diplomat's kernel code mapping
- Consider adding debug output to dump the PTE chain for VA 0x438AE498 when the instruction page fault occurs
- Check `HandlePageFaultAsm` to see if it modifies page tables for addresses in the kernel code range

## Other Known Issues (Lower Priority)

1. **VirtIO GPU ATTACH_BACKING failed (0x1200)**: The framebuffer PA is 0x41000000, which is below 0x80000000 (start of RAM on RISC-V QEMU virt). This physical address is invalid for RISC-V. Likely an ARM64 constant that needs arch-specific handling.

2. **mmio_constants.go has ARM64 constants**: `kmazarin/kmazarin/mmio_constants.go` has `UartBase = 0xFFFFFFFF09000000` (ARM64 PL011) without a build tag. RISC-V NS16550 is at `0xFFFFFFFF10000000`. Used by `bottom_half.go:551` for UART interrupt handling.

3. **VirtIO Block needs MMIO transport**: Kmazarin's block driver only scans PCI. RISC-V QEMU virt uses VirtIO MMIO at 0x10008000 (diplomat found and used it). Kmazarin needs MMIO VirtIO block support.

## Files Modified This Session

### `kmazarin/pci/ecam_riscv64.go` (MODIFIED)
Changed `pciEcamBase` from physical `0x30000000` to linear-mapped VA `0xFFFFFFFF30000000`:
```go
var pciEcamBase uintptr = 0xFFFFFFFF30000000
```
PCI_MMIO_BASE (0x40000000) left as physical address because it's written to PCI BARs.

## Files Modified in Previous Sessions (Still Active)

- `runtime-patches/rt0_linux_riscv64.s` — Kmazarin overlay with re-entry detection (prints Z+SP+r+RA)
- `cmd/gen-overlay/main.go` — Added rt0_linux_riscv64.s to kmazarin RISC-V overlay
- `cmd/fix-go-elf/main.go` — Added `-no-bootstrap` flag
- `Taskfile.yml` — kmazarin-riscv64 uses `fix-go-elf -no-bootstrap`
- `kmazarin/console/breadcrumb_riscv64.go` — UART VA 0xFFFFFFFF10000000
- `kmazarin/kmazarin/early_init_riscv64.go` — UART VA 0xFFFFFFFF10000000
- `diplomat/main/kernelvm_riscv64.go` — Fixed KernelHeapStart/End to Sv48 canonical
- `shared/constants/addresses_riscv64.go` — KernelUartBase for RISC-V NS16550
- `shared/constants/addresses_arm64.go` — KernelUartBase for ARM64 PL011
- `shared/constants/addresses_amd64.go` — KernelUartBase for x86_64 COM1
- `shared/constants/addresses.go` — Removed arch-generic KernelUartBase
- `kmazarin/kmazarin/exceptions_riscv64.s` — Debug markers in trap handler
- `runtime-patches/sys_linux_riscv64.s` — WFI→NOP, clone debug markers

## Build & Run

```bash
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 /opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task clean
/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task disk-riscv64-minimal
/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task diplomat-riscv64
/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task run-diplomat-riscv64 TIMEOUT=30 VIRTIO_TRANSPORT=mmio
GOTOOLCHAIN=auto /opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

## Key Architecture Notes

- RISC-V Sv48 page tables: L3[0] = kernel code, L3[511] = linear map + heap
- Linear map: VA 0xFFFFFFFF00000000-0xFFFFFFFFFFFFFFFF → PA 0x00-0xFFFFFFFF
- Kmazarin VAs: 0x43800000 (L3=0, L2=1, L1=0x1C+), 32-bit addresses
- Diplomat PT root at PA 0x94200000, unified pool starts at 0x9434C000
- KernelMMIOOffset = 0xFFFFFFFF00000000
- NS16550 UART: PA 0x10000000 → VA 0xFFFFFFFF10000000
- PCI ECAM: PA 0x30000000 → VA 0xFFFFFFFF30000000
- EBREAK used for syscalls (ECALL goes to M-mode/OpenSBI, not our trap handler)
- Go g register = X27, SP = X2, RA = X1
