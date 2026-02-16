# Continuation 8: ARM64 initProcessL0 Page Table Regression

## Symptom

ARM64 kmazarin crashes at `[Launch] /dapope.elf` with:
```
[SWITCH_PT] L3[0] INVALID before switch! l0PA=0x00000000B824D000 ASID=0000000000000000 L3[0]=0x0000000000000000
  L3[0]=0x0000000000000000
  L3[1]=0x0000000000000000
  L3[2]=0x0000000000000000
  L3[3]=0x0000000000000000
```

The process page table's L3 (root, L0 in our naming) is entirely zeros. Userspace can't launch because there are no kernel mappings in the process page table.

## Root Cause

`initProcessL0` in `kmazarin/kmem/paging.go` creates a new L0 (root) table for each process. On RISC-V (single SATP), it must copy kernel L0 entries into the process table. The RISC-V work modified this function and likely broke the ARM64 path (which uses TTBR0/TTBR1 split and has different requirements).

## Key Files

- `kmazarin/kmem/paging.go` — `initProcessL0` (shared, arch-conditional logic)
- `kmazarin/kmem/paging_arm64.go` — ARM64 page table helpers
- `kmazarin/kmem/paging_riscv64.go` — RISC-V page table helpers
- `kmazarin/ksyscall/launch.go` — calls `initProcessL0` during ELF launch

## Investigation

1. Read `initProcessL0` and trace the ARM64 code path
2. Compare with the last known-working ARM64 behavior (git log for paging.go changes)
3. The fix is likely: ensure ARM64 path populates L0 entries correctly (ARM64 uses TTBR0 for user, TTBR1 for kernel — process L0 only needs user mappings, not kernel copies)

## Also Fixed This Session

- `saveTextChecksum` — added no-op stubs in `platform_arm64.go` and `platform_amd64.go`
- `verifyCodeIntegrityKmazarin` — added `runtime.GOARCH != "riscv64"` early return (hardcoded RISC-V addresses)
- VirtIO Input PCI INTx via PLIC — working on RISC-V (25 K + 305 M breadcrumbs confirmed)
