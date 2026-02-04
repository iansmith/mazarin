# Diplomat ARM64 UEFI Port - Implementation Plan

## Overview

Port the Diplomat UEFI bootloader from x86_64 to ARM64. This enables:
1. Running on Apple Silicon via Tart (Apple Virtualization.framework)
2. Native ARM64 UEFI boot on real hardware
3. Shared codebase for both architectures

## Current State

Diplomat is a UEFI bootloader written in Go that:
- Loads as a PE32+ UEFI application
- Mounts FAT32 filesystem from boot device
- Loads kmazarin.elf kernel into memory
- Sets up page tables and jumps to kernel

### Existing Architecture-Specific Files (x86_64)

| File | Purpose |
|------|---------|
| `entry_amd64.s` | UEFI entry point, g0/m0 init, TLS setup |
| `uefi_calls_amd64.s` | UEFI function call wrappers (MS x64 ABI) |
| `tls_amd64.s` | TLS configuration via WRMSR (FS segment) |
| `minimal_test_amd64.s` | Phase 0 toolchain validation |
| `platform_amd64.go` | Platform function table instances |
| `pagetable.go` | x86_64 4-level page table implementation |

### Portable Files (No Changes Needed)

- `main.go` - Entry point, EFI types/constants
- `platform.go` - Platform abstraction (PlatformOps, BootSequence)
- `elf_loader.go` - ELF kernel loading
- `mmap.go`, `dmalloc.go`, `span.go` - Memory management
- `uefi_blockdev.go`, `fat32_walk.go` - Filesystem access
- `uefi_protocol.go` - Protocol discovery
- `syscall_dispatcher.go`, `syscalls.go`, `futex.go` - Syscall support

---

## Key Technical Differences

| Aspect | x86_64 | ARM64 |
|--------|--------|-------|
| PE Machine Type | 0x8664 | 0xAA64 |
| ELF Machine | EM_X86_64 | EM_AARCH64 |
| Go ABI | SysV x64 (RDI,RSI,RDX,RCX) | AAPCS64 (X0-X7) |
| UEFI ABI | MS x64 (RCX,RDX,R8,R9) | AAPCS64 (X0-X7) |
| ABI Translation | **Required** | **Not needed!** |
| TLS Register | FS segment (MSR 0xC0000100) | TPIDR_EL0 |
| g Register | R14 (manual) | R28 (Go convention) |
| Page Table Reg | CR3 | TTBR0_EL1 / TTBR1_EL1 |
| Page Table Format | PML4 (4-level, 9-9-9-9-12) | 4-level (9-9-9-9-12 with 4KB) |
| Write Protect | CR0.WP bit | AP bits in descriptors |
| Exception Level | Ring 0 | EL1 (or EL2 for hypervisor) |

### ARM64 UEFI Simplification

ARM64 UEFI uses AAPCS64 - the **same** calling convention as Go on ARM64. This means:
- No register shuffling between Go and UEFI calls
- Assembly wrappers are trivial (just `BL` to function pointer)
- Significant reduction in complexity vs x86_64

---

## Implementation Plan

### Phase 1: Build Infrastructure

#### 1.1 Update elf2pe Tool

**File:** `cmd/elf2pe/main.go`

Add ARM64 support:

```go
const (
    IMAGE_FILE_MACHINE_AMD64 = 0x8664
    IMAGE_FILE_MACHINE_ARM64 = 0xAA64  // Add this
)

// In convertELFtoPE():
var machineType uint16
switch elfFile.Machine {
case elf.EM_X86_64:
    machineType = IMAGE_FILE_MACHINE_AMD64
case elf.EM_AARCH64:
    machineType = IMAGE_FILE_MACHINE_ARM64
default:
    return fmt.Errorf("unsupported ELF machine type: %v", elfFile.Machine)
}

// Update entry point symbol search for ARM64:
entrySymbols := []string{
    "main._efi_main_asm",      // x86_64
    "main._efi_main_arm64",    // ARM64
    // ... etc
}
```

#### 1.2 Update Taskfile.yml

Add ARM64 build targets:

```yaml
diplomat-arm64:
  desc: Build Diplomat UEFI bootloader for ARM64
  cmds:
    - CGO_ENABLED=0 GOOS=linux GOARCH=arm64 {{.GO}} build
        -overlay={{.BUILD_DIR}}/diplomat-linux-overlay.json
        -ldflags="-checklinkname=0"
        -o {{.BUILD_DIR}}/diplomat-arm64.elf ./diplomat/main
    - {{.GO}} tool elf2pe {{.BUILD_DIR}}/diplomat-arm64.elf {{.BUILD_DIR}}/BOOTAA64.EFI
```

### Phase 2: Assembly Files

#### 2.1 Entry Point (`entry_arm64.s`)

**File:** `diplomat/main/entry_arm64.s`

```asm
#include "textflag.h"

// _efi_main_arm64 is the UEFI entry point for ARM64
// UEFI passes: X0 = ImageHandle, X1 = SystemTable
// ARM64 UEFI uses AAPCS64 - same as Go!
TEXT main·_efi_main_arm64(SB), NOSPLIT|NOFRAME, $0
    // Step 1: Save UEFI parameters to globals
    MOVD    X0, main·imageHandle(SB)
    MOVD    X1, main·systemTable(SB)

    // Step 2: Initialize g0/m0
    // Set g register (R28 on ARM64) to runtime.g0
    MOVD    $runtime·g0(SB), g  // g = R28

    // Set up stack guards
    MOVD    RSP, R0
    SUB     $65536, R0, R0      // 64KB guard
    MOVD    R0, 16(g)           // g0.stackguard0
    MOVD    R0, 24(g)           // g0.stackguard1
    MOVD    R0, 0(g)            // g0.stack.lo
    MOVD    RSP, 8(g)           // g0.stack.hi

    // Link g0 and m0
    MOVD    $runtime·m0(SB), R0
    MOVD    R0, 48(g)           // g0.m = &m0
    MOVD    g, (R0)             // m0.g0 = &g0

    // Step 3: Set up TLS
    // Store g at tlsBlock[0], set TPIDR_EL0 = tlsBlock + 8
    // So that loading from [TPIDR_EL0, #-8] gives g
    MOVD    $main·tlsBlock(SB), R0
    MOVD    g, (R0)             // Store g at tlsBlock[0]
    ADD     $8, R0, R0
    MSR     R0, TPIDR_EL0       // Set TLS base

    // Step 4: Call DiplomatEntry
    BL      main·DiplomatEntry(SB)

    // Should not return
    MOVD    $0, R0
    RET
```

#### 2.2 UEFI Call Wrappers (`uefi_calls_arm64.s`)

**File:** `diplomat/main/uefi_calls_arm64.s`

ARM64 UEFI uses AAPCS64 - same as Go! Wrappers are trivial:

```asm
#include "textflag.h"

// ueficall_OutputString calls UEFI OutputString
// Go: func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
// UEFI: EFI_STATUS OutputString(EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL *This, CHAR16 *String)
TEXT ·ueficall_OutputString(SB), NOSPLIT, $32
    MOVD    conout+0(FP), R0        // This (protocol pointer)

    // Build UCS-2 string on stack: [char, 0x0000]
    MOVHU   char+8(FP), R1
    MOVD    RSP, R2
    MOVHU   R1, (R2)                // char
    MOVHU   ZR, 2(R2)               // null terminator
    MOVD    R2, R1                  // String pointer

    // Load OutputString function pointer (offset 8 in protocol)
    MOVD    8(R0), R2
    BLR     R2                      // Call OutputString

    RET

// uefiAllocatePages calls UEFI AllocatePages
// Go: func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS
TEXT ·uefiAllocatePages(SB), NOSPLIT, $0
    MOVW    allocType+0(FP), R0
    MOVW    memType+4(FP), R1
    MOVD    pages+8(FP), R2
    MOVD    memory+16(FP), R3

    // Get BootServices->AllocatePages (offset 40)
    MOVD    main·systemTable(SB), R4
    MOVD    96(R4), R4              // BootServices
    MOVD    40(R4), R4              // AllocatePages
    BLR     R4

    MOVD    R0, ret+24(FP)
    RET

// Similar wrappers for: FreePages, HandleProtocol, BlockIORead,
// BlockIOWrite, GetMemoryMap, ExitBootServices
```

#### 2.3 TLS Setup (`tls_arm64.s`)

**File:** `diplomat/main/tls_arm64.s`

```asm
#include "textflag.h"

// setupTLS sets TPIDR_EL0 for Go TLS support
// Go: func setupTLS(tlsAddr uintptr)
TEXT ·setupTLS(SB), NOSPLIT, $0-8
    MOVD    tlsAddr+0(FP), R0
    MSR     R0, TPIDR_EL0
    RET
```

### Phase 3: Go Files

#### 3.1 Platform Instance (`platform_arm64.go`)

**File:** `diplomat/main/platform_arm64.go`

```go
//go:build arm64

package main

// ARM64 UEFI function declarations (implemented in uefi_calls_arm64.s)
func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS
func uefiFreePages(memory uint64, pages uint64) EFI_STATUS
func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS
func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
func uefiExitBootServices() EFI_STATUS

// ARM64 page table functions (implemented in pagetable_arm64.go)
func readTTBR0() uint64
func writeTTBR0(val uint64)
func invalidateTLB()

var defaultPlatform = PlatformOps{
    PrintChar:    printChar,
    DebugPortOut: debugPortOutARM64,

    AllocatePages: func(allocType, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS {
        return uefiAllocatePages(allocType, memoryType, pages, memory)
    },
    FreePages: func(memory uint64, pages uint64) EFI_STATUS {
        return uefiFreePages(memory, pages)
    },
    ZeroMemory: zeroMemoryARM64,

    ReadCR3:  readTTBR0,   // ARM64 equivalent
    WriteCR3: writeTTBR0,
    DisableWriteProtect: func() {}, // Not needed on ARM64
    EnableWriteProtect:  func() {},

    ExitBootServices: uefiExitBootServices,
    HandleProtocol: func(handle, protocol, iface uintptr) EFI_STATUS {
        return uefiHandleProtocol(handle, protocol, iface)
    },
    // ... etc
}

var defaultBootSequence = BootSequence{
    InitSpans:       initializeSpans,
    GetBlockDevice:  GetBootDeviceBlockIO,
    MountFilesystem: fat32Mount,
    LoadKernel:      LoadKernelFromFS,
    MapKernel:       addKernelMappingToCurrentPT_ARM64,
    JumpToKernel:    jumpToKernelARM64,
}

//go:nosplit
func debugPortOutARM64(c byte) {
    // ARM64 debug output - could use PL011 UART or semihosting
    // For now, no-op (UEFI console is primary output)
}

//go:nosplit
func zeroMemoryARM64(addr, size uint64) {
    p := (*[1 << 30]byte)(unsafe.Pointer(uintptr(addr)))
    for i := uint64(0); i < size; i++ {
        p[i] = 0
    }
}
```

#### 3.2 Page Tables (`pagetable_arm64.go`)

**File:** `diplomat/main/pagetable_arm64.go`

```go
//go:build arm64

package main

import "unsafe"

// ARM64 page table constants (4KB granule, 48-bit VA)
const (
    PageSize4K  = 4096
    PageSize2M  = 2 * 1024 * 1024
    PageSize1G  = 1024 * 1024 * 1024

    // Descriptor bits
    ARM64_PTE_VALID     = 1 << 0
    ARM64_PTE_TABLE     = 1 << 1  // For non-leaf entries
    ARM64_PTE_BLOCK     = 0 << 1  // For block (2M/1G) entries
    ARM64_PTE_PAGE      = 1 << 1  // For 4K page entries
    ARM64_PTE_AF        = 1 << 10 // Access flag
    ARM64_PTE_SH_INNER  = 3 << 8  // Inner shareable
    ARM64_PTE_AP_RW     = 0 << 6  // Read-write at EL1
    ARM64_PTE_AP_RO     = 2 << 6  // Read-only at EL1
    ARM64_PTE_ATTR_MEM  = 0 << 2  // Normal memory (MAIR index 0)
    ARM64_PTE_ATTR_DEV  = 1 << 2  // Device memory (MAIR index 1)

    // Address mask for descriptors
    ARM64_ADDR_MASK = 0x0000FFFFFFFFF000
)

// ARM64 virtual address breakdown (4KB granule, 48-bit)
// [47:39] = L0 index (9 bits)
// [38:30] = L1 index (9 bits)
// [29:21] = L2 index (9 bits)
// [20:12] = L3 index (9 bits)
// [11:0]  = page offset (12 bits)

func l0Index(vaddr uint64) uint64 { return (vaddr >> 39) & 0x1FF }
func l1Index(vaddr uint64) uint64 { return (vaddr >> 30) & 0x1FF }
func l2Index(vaddr uint64) uint64 { return (vaddr >> 21) & 0x1FF }
func l3Index(vaddr uint64) uint64 { return (vaddr >> 12) & 0x1FF }

// addKernelMappingToCurrentPT_ARM64 adds kernel mappings to UEFI's page tables
func addKernelMappingToCurrentPT_ARM64(virtBase, physBase, size uint64) error {
    size = (size + PageSize2M - 1) &^ (PageSize2M - 1)
    numPages := size / PageSize2M

    // Read current TTBR0 (UEFI's page tables)
    ttbr0 := readTTBR0()
    l0TablePhys := ttbr0 &^ 0xFFF

    l0Table := (*[512]uint64)(unsafe.Pointer(uintptr(l0TablePhys)))

    // Allocate L1, L2 tables for kernel mapping
    l1Phys := physBase + size - 2*PageSize4K
    l2Phys := physBase + size - 1*PageSize4K

    plat.ZeroMemory(l1Phys, PageSize4K)
    plat.ZeroMemory(l2Phys, PageSize4K)

    l1Table := (*[512]uint64)(unsafe.Pointer(uintptr(l1Phys)))
    l2Table := (*[512]uint64)(unsafe.Pointer(uintptr(l2Phys)))

    // Calculate indices
    kernelL0Idx := l0Index(virtBase)
    kernelL1Idx := l1Index(virtBase)
    kernelL2Idx := l2Index(virtBase)

    // Set L0 entry -> L1 table
    l0Table[kernelL0Idx] = l1Phys | ARM64_PTE_VALID | ARM64_PTE_TABLE

    // Set L1 entry -> L2 table
    l1Table[kernelL1Idx] = l2Phys | ARM64_PTE_VALID | ARM64_PTE_TABLE

    // Map 2MB blocks in L2
    virtOffset := virtBase & (PageSize2M - 1)
    blockPhysBase := physBase - virtOffset

    for i := uint64(0); i < numPages; i++ {
        l2Idx := kernelL2Idx + i
        if l2Idx >= 512 {
            return &blockDevError{"kernel mapping spans multiple L1 entries"}
        }
        physAddr := blockPhysBase + i*PageSize2M
        // Block descriptor: valid, block, AF, inner shareable, RW, normal memory
        l2Table[l2Idx] = physAddr | ARM64_PTE_VALID | ARM64_PTE_BLOCK |
                         ARM64_PTE_AF | ARM64_PTE_SH_INNER | ARM64_PTE_AP_RW | ARM64_PTE_ATTR_MEM
    }

    // Invalidate TLB
    invalidateTLB()

    return nil
}

// Assembly declarations
func readTTBR0() uint64
func writeTTBR0(val uint64)
func invalidateTLB()
```

**File:** `diplomat/main/pagetable_arm64.s`

```asm
#include "textflag.h"

// func readTTBR0() uint64
TEXT ·readTTBR0(SB), NOSPLIT, $0-8
    MRS     TTBR0_EL1, R0
    MOVD    R0, ret+0(FP)
    RET

// func writeTTBR0(val uint64)
TEXT ·writeTTBR0(SB), NOSPLIT, $0-8
    MOVD    val+0(FP), R0
    MSR     R0, TTBR0_EL1
    ISB     $15
    RET

// func invalidateTLB()
TEXT ·invalidateTLB(SB), NOSPLIT, $0
    TLBI    VMALLE1
    DSB     $15
    ISB     $15
    RET
```

### Phase 4: Testing

#### 4.1 Build Verification

```bash
# Build ARM64 diplomat
$GO tool task diplomat-arm64

# Verify PE format
file build/BOOTAA64.EFI
# Expected: PE32+ executable (EFI application) Aarch64
```

#### 4.2 QEMU Testing (Before Tart)

```bash
# Test with QEMU using UEFI firmware
qemu-system-aarch64 -M virt -cpu cortex-a72 -m 4G \
    -bios /path/to/AAVMF_CODE.fd \
    -drive file=build/disk.img,format=raw \
    -serial stdio
```

#### 4.3 Tart Integration

Once QEMU testing passes, integrate with Tart:

```bash
# Create Tart VM with custom EFI
tart create --from-ipsw none myvm
# Mount EFI partition and copy BOOTAA64.EFI
# Configure to boot from it
```

---

## File Checklist

### New Files to Create

- [ ] `diplomat/main/entry_arm64.s`
- [ ] `diplomat/main/uefi_calls_arm64.s`
- [ ] `diplomat/main/tls_arm64.s`
- [ ] `diplomat/main/platform_arm64.go`
- [ ] `diplomat/main/pagetable_arm64.go`
- [ ] `diplomat/main/pagetable_arm64.s`

### Files to Modify

- [ ] `cmd/elf2pe/main.go` - Add ARM64 machine type
- [ ] `Taskfile.yml` - Add diplomat-arm64 target

### Optional/Future

- [ ] `diplomat/main/minimal_test_arm64.s` - Phase 0 validation
- [ ] Runtime patches for ARM64 diplomat

---

## Notes

### UEFI on ARM64

- UEFI firmware provides page tables with identity mapping
- We graft kernel mapping into existing tables (like x86_64)
- TCR_EL1 is already configured by UEFI
- MAIR_EL1 memory attributes are already set

### Tart/Virtualization.framework

- Tart uses VZEFIBootLoader for full UEFI boot
- EFI application placed at `/EFI/BOOT/BOOTAA64.EFI`
- Disk image must be GPT with EFI System Partition

### Shared Code with Cardinal

Cardinal's ARM64 code can be referenced for:
- Page table descriptor format
- TLB invalidation sequences
- Exception level handling
- Memory barrier patterns
