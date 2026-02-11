# RISC-V Diplomat Boot Implementation Plan

## Overview

This plan implements RISC-V boot for the mazzy project's diplomat bootloader.
Diplomat is loaded by QEMU via `-kernel` (OpenSBI handles M-mode setup and drops
to S-mode before entering diplomat). Diplomat then:
1. Sets up UART output
2. Builds Sv48 page tables and enables the MMU
3. Bootstraps the Go runtime
4. Parses the FDT to discover RAM, CPUs
5. Reads kmazarin kernel from a VirtIO MMIO block device (FAT32)
6. Sets up kernel VM, demand paging, auxv
7. Jumps to kmazarin

**Working directory**: `~/mazzy-riscv` (git worktree on branch `riscv-boot`)
**Base branch**: `feature/port-x86-riscv`

---

## Prerequisites / Environment

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

**Build & test**:
```bash
$GO tool task run-diplomat-riscv64 TIMEOUT=10
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

**CRITICAL**: Never read serial log files directly. Always use `$GO tool safe-serial-read`.

---

## Key Constants and Addresses

### RISC-V QEMU virt Platform
| Item | Address |
|------|---------|
| RAM start | `0x80000000` |
| RAM end (2GB) | `0xFFFFFFFF` |
| NS16550A UART | `0x10000000` |
| PLIC | `0x0C000000` |
| CLINT | `0x02000000` |
| PCI ECAM | `0x30000000` |
| PCI MMIO window | `0x40000000` |
| VirtIO MMIO slots | `0x10001000`-`0x10008000` |
| OpenSBI reserved | `0x80000000`-`0x80200000` |

### Kernel Virtual Addresses
| Item | Address |
|------|---------|
| KernelMMIOOffset (linear map) | `0xFFFFFFFF00000000` |
| KernelVABase | `0xFFFFFFFF40000000` |
| Kmazarin load VA | `0xFFFFFFFF43800000` |
| Kernel stacks VA | `0xFFFFFFFF43E00000` |
| UART via linear map | `0xFFFFFFFF10000000` |
| PLIC via linear map | `0xFFFFFFFF0C000000` |

### Sv48 Page Table Structure
```
L3 (root): index = VA[47:39], 512 entries, each covers 512GB
  L2:      index = VA[38:30], 512 entries, each covers 1GB  (can be gigapage leaf)
    L1:    index = VA[29:21], 512 entries, each covers 2MB  (can be megapage leaf)
      L0:  index = VA[20:12], 512 entries, each covers 4KB  (always leaf)
```

**SATP register format**: `MODE[63:60] | ASID[59:44] | PPN[43:0]`
- MODE=9 for Sv48
- PPN = physical page number of L3 root table (PA >> 12)

### PTE format (RISC-V)
```
Bits [63:54] = reserved (must be 0 unless Svpbmt)
Bits [53:10] = PPN (44 bits, physical page number)
Bits [9:8]   = RSW (reserved for software)
Bit  7       = D (Dirty)
Bit  6       = A (Accessed)
Bit  5       = G (Global)
Bit  4       = U (User)
Bit  3       = X (Execute)
Bit  2       = W (Write)
Bit  1       = R (Read)
Bit  0       = V (Valid)
```

**Branch PTE**: V=1, R=W=X=0 (points to next level table)
**Leaf PTE**: V=1, at least one of R/W/X set

---

## Phase 1: Assembly Entry + UART Output

**Goal**: Diplomat prints a character to serial output when loaded by OpenSBI.

### File: `diplomat/main/entry_riscv64.s` — COMPLETE REWRITE

Replace the entire file. The current file is UEFI-based and saves ImageHandle/SystemTable.

```asm
// diplomat/main/entry_riscv64.s
// RISC-V 64-bit entry point — entered from OpenSBI in S-mode
//
// OpenSBI provides:
//   A0 = hart ID (always 0 for primary boot hart)
//   A1 = FDT (Flattened Device Tree) physical address
//   SATP = 0 (bare mode, no page tables)
//   SIE = 0 (interrupts disabled)
//   SP = OpenSBI-provided stack (unreliable size)
//
// This file:
//   1. Saves A0/A1 to s-registers
//   2. Prints "D" to UART (proof of life)
//   3. Sets up diplomat's stack in known RAM
//   4. Builds Sv48 page tables
//   5. Enables MMU (csrw satp)
//   6. Jumps to high-memory Go entry

#include "textflag.h"

// UART NS16550A register addresses (physical)
#define UART_BASE   0x10000000
#define UART_THR    0           // Transmit Holding Register
#define UART_LSR    5           // Line Status Register
#define UART_LSR_THRE 0x20     // Transmitter Holding Register Empty

// Sv48 mode for SATP
#define SATP_MODE_SV48  (9 << 60)

// PTE flag bits
#define PTE_V    (1 << 0)
#define PTE_R    (1 << 1)
#define PTE_W    (1 << 2)
#define PTE_X    (1 << 3)
#define PTE_A    (1 << 6)
#define PTE_D    (1 << 7)
#define PTE_G    (1 << 5)

// Kernel linear map offset
#define KERNEL_VA_OFFSET  0xFFFFFFFF00000000

// diplomat binary is loaded at 0x80200000 by OpenSBI (-kernel flag)
// Stack and page tables go after the binary. Assume diplomat is < 16MB.
// Reserve space starting at 0x81200000:
//   PT pool:   0x81200000 - 0x8120FFFF (16 pages = 64KB for page tables)
//   G0 stack:  0x81210000 - 0x81217FFF (8 pages = 32KB)
//   Exc stack: 0x81218000 - 0x8121BFFF (4 pages = 16KB)
// These MUST be below the bump allocator start address.

#define PT_POOL_BASE     0x81200000
#define G0_STACK_BASE    0x81210000
#define G0_STACK_SIZE    0x8000       // 32KB
#define EXC_STACK_BASE   0x81218000
#define EXC_STACK_SIZE   0x4000       // 16KB
#define BUMP_ALLOC_START 0x81220000   // bump allocator starts here

// Offsets into Go's g struct (must match Go runtime)
#define g_stack_lo    0
#define g_stack_hi    8
#define g_stackguard0 16
#define g_stackguard1 24

TEXT _rt0_riscv64_linux(SB), NOSPLIT|NOFRAME, $0
    // Save OpenSBI parameters
    MOV     A0, S0          // S0 = hart ID
    MOV     A1, S1          // S1 = FDT physical address

    // ---- Step 1: Print 'D' to UART (proof of life) ----
    MOV     $UART_BASE, T0
uart_wait_1:
    MOVBU   5(T0), T1       // Read LSR
    AND     $UART_LSR_THRE, T1
    BEQ     T1, ZERO, uart_wait_1
    MOV     $'D', T1
    MOVB    T1, (T0)        // Write 'D' to THR

    // ---- Step 2: Set up our own stack ----
    // Use g0 stack top as initial SP (physical address, no MMU yet)
    MOV     $(G0_STACK_BASE + G0_STACK_SIZE), SP

    // ---- Step 3: Zero page table pool (64KB) ----
    MOV     $PT_POOL_BASE, T0
    MOV     $(PT_POOL_BASE + 16*4096), T1  // 16 pages
zero_pt_loop:
    MOV     ZERO, (T0)
    ADD     $8, T0
    BNE     T0, T1, zero_pt_loop

    // ---- Step 4: Build Sv48 page tables ----
    // Allocate pages from PT pool:
    //   Page 0 (0x81200000): L3 root table
    //   Page 1 (0x81201000): L2 table for identity map (low addresses)
    //   Page 2 (0x81202000): L2 table for kernel high VA (0xFFFFFFFF...)
    //   Page 3 (0x81203000): L1 table for linear map detail (if needed)
    //   Page 4+ : L1/L0 tables for stacks, kernel code

    // --- L3 root (page 0) ---
    MOV     $PT_POOL_BASE, S2       // S2 = L3 root PA

    // L3 entry for identity map: VA[47:39] of 0x80000000 = index 1
    // Points to L2 table (page 1)
    MOV     $(PT_POOL_BASE + 0x1000), T0   // L2 table for identity map
    SRL     $12, T0, T0                     // PPN
    SLL     $10, T0, T0                     // shift to PTE position
    OR      $PTE_V, T0, T0                  // Branch PTE (V only, no R/W/X)
    MOV     $PT_POOL_BASE, T1
    ADD     $(1 * 8), T1                    // L3[1] (index 1 covers 0x40_0000_0000-0x7F_FFFF_FFFF)
    // Wait — VA 0x80000000: bit 47:39 = 0x80000000 >> 39 = 0 (only 1 bit set at bit 31)
    // Actually for Sv48:
    //   VA 0x80000000 → bits[47:39] = 0 (since 0x80000000 < 2^39)
    //   So L3 index = 0
    MOV     $PT_POOL_BASE, T1
    ADD     $(0 * 8), T1                    // L3[0]
    MOV     T0, (T1)

    // L3 entry for kernel VA: VA[47:39] of 0xFFFFFFFF00000000
    // 0xFFFFFFFF00000000 >> 39 = sign-extended... need to think carefully.
    // Sv48 uses bits [47:0] of VA. VA 0xFFFFFFFF00000000:
    //   bits[47:39] = 0xFFFFFFFF00000000[47:39]
    //   0xFFFFFFFF00000000 = ...1111_1111_1111_1111_1111_1111_0000...
    //   bit 47 = 1, bits 46:39 = 11111111 = 0xFF
    //   L3 index = 0x1FF = 511
    // Points to L2 table (page 2)
    MOV     $(PT_POOL_BASE + 0x2000), T0   // L2 table for kernel
    SRL     $12, T0, T0
    SLL     $10, T0, T0
    OR      $PTE_V, T0, T0
    MOV     $PT_POOL_BASE, T1
    ADD     $(511 * 8), T1                  // L3[511]
    MOV     T0, (T1)

    // --- L2 table for identity map (page 1) ---
    // Map 0x80000000 as 1GB gigapage leaf
    // VA 0x80000000: L2 index = (0x80000000 >> 30) & 0x1FF = 2
    // PA = 0x80000000, PPN = 0x80000000 >> 12 = 0x80000
    MOV     $0x80000, T0                    // PPN of 0x80000000
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T0, T0
    MOV     $(PT_POOL_BASE + 0x1000), T1    // L2 identity table
    ADD     $(2 * 8), T1                     // L2[2]
    MOV     T0, (T1)

    // Also identity-map 0x00000000-0x3FFFFFFF (1GB) for MMIO (UART at 0x10000000)
    // L2[0]: PA=0, 1GB gigapage
    MOV     $0, T0                          // PPN of 0x00000000
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T0, T0
    MOV     $(PT_POOL_BASE + 0x1000), T1
    ADD     $(0 * 8), T1                     // L2[0]
    MOV     T0, (T1)

    // --- L2 table for kernel VA (page 2) ---
    // Linear map: VA 0xFFFFFFFF00000000 + PA → PA
    // L2 index for VA 0xFFFFFFFF00000000: (VA >> 30) & 0x1FF
    // VA[38:30] of 0xFFFFFFFF00000000 = 0b111111100 = 0x1FC
    // So L2[0x1FC] maps PA 0x00000000 (1GB, MMIO region)
    // L2[0x1FD] maps PA 0x40000000 (1GB, PCI MMIO)
    // L2[0x1FE] maps PA 0x80000000 (1GB, RAM first half)
    // L2[0x1FF] maps PA 0xC0000000 (1GB, RAM second half / not present)

    // L2[0x1FC]: PA 0x00000000 (UART, PLIC, CLINT)
    MOV     $0, T0
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T0, T0
    MOV     $(PT_POOL_BASE + 0x2000), T1
    ADD     $(0x1FC * 8), T1
    MOV     T0, (T1)

    // L2[0x1FD]: PA 0x40000000 (PCI MMIO window)
    MOV     $0x40000, T0                    // PPN of 0x40000000
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T0, T0
    MOV     $(PT_POOL_BASE + 0x2000), T1
    ADD     $(0x1FD * 8), T1
    MOV     T0, (T1)

    // L2[0x1FE]: PA 0x80000000 (RAM)
    MOV     $0x80000, T0                    // PPN of 0x80000000
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T0, T0
    MOV     $(PT_POOL_BASE + 0x2000), T1
    ADD     $(0x1FE * 8), T1
    MOV     T0, (T1)

    // L2[0x1FF]: PA 0xC0000000 (RAM upper 1GB)
    MOV     $0xC0000, T0                    // PPN of 0xC0000000
    SLL     $10, T0, T0
    OR      $(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T0, T0
    MOV     $(PT_POOL_BASE + 0x2000), T1
    ADD     $(0x1FF * 8), T1
    MOV     T0, (T1)

    // ---- Step 5: Enable MMU ----
    // SATP = (MODE=9 << 60) | (PPN of L3 root)
    MOV     S2, T0                          // L3 root PA
    SRL     $12, T0, T0                     // PPN
    MOV     $SATP_MODE_SV48, T1
    OR      T0, T1, T0
    // csrw satp, t0
    // RISC-V Go assembler doesn't know csrw, use WORD encoding
    // csrw satp, t0 = csrrw x0, satp, t0
    // satp = CSR 0x180, t0 = x5
    // Encoding: imm[11:0]=0x180, rs1=x5, funct3=001, rd=x0, opcode=1110011
    // = 0x18029073
    WORD    $0x18029073                     // csrw satp, t0
    // sfence.vma (flush TLB)
    WORD    $0x12000073                     // sfence.vma zero, zero

    // ---- Step 6: Print 'P' via high-VA UART (verify MMU works) ----
    MOV     $0xFFFFFFFF10000000, T0         // UART via linear map
uart_wait_2:
    MOVBU   5(T0), T1
    AND     $UART_LSR_THRE, T1
    BEQ     T1, ZERO, uart_wait_2
    MOV     $'P', T1
    MOVB    T1, (T0)

    // ---- Step 7: Jump to high-memory Go bootstrap ----
    // Convert SP to high VA
    MOV     $KERNEL_VA_OFFSET, T0
    ADD     T0, SP, SP

    // Store hart ID and FDT address as globals (via high VA)
    MOV     $·savedHartID(SB), T0
    MOV     S0, (T0)
    MOV     $·savedFDTAddr(SB), T0
    MOV     S1, (T0)

    // Store physical addresses of key regions for Go code
    MOV     $·ptPoolBase(SB), T0
    MOV     $PT_POOL_BASE, T1
    MOV     T1, (T0)

    MOV     $·bumpAllocStart(SB), T0
    MOV     $BUMP_ALLOC_START, T1
    MOV     T1, (T0)

    MOV     $·g0StackPA(SB), T0
    MOV     $G0_STACK_BASE, T1
    MOV     T1, (T0)

    MOV     $·excStackPA(SB), T0
    MOV     $EXC_STACK_BASE, T1
    MOV     T1, (T0)

    // Initialize g0 and TLS (same pattern as current entry_riscv64.s)
    // g0 is a Go global — its address is known at link time
    MOV     $runtime·g0(SB), S10    // S10 = g0 address (X26 is Go's g register)

    // Set g0.stack bounds (using high-VA addresses)
    MOV     $(G0_STACK_BASE), T0
    ADD     $KERNEL_VA_OFFSET, T0, T0
    MOV     T0, g_stack_lo(S10)             // g0.stack.lo
    MOV     $(G0_STACK_BASE + G0_STACK_SIZE), T0
    ADD     $KERNEL_VA_OFFSET, T0, T0
    MOV     T0, g_stack_hi(S10)             // g0.stack.hi

    // Stack guards
    MOV     $(G0_STACK_BASE + 1024), T0     // guard = lo + 1024
    ADD     $KERNEL_VA_OFFSET, T0, T0
    MOV     T0, g_stackguard0(S10)
    MOV     T0, g_stackguard1(S10)

    // Link g0 and m0
    MOV     $runtime·m0(SB), T0
    MOV     S10, 48(T0)                     // m0.g0 = &g0 (offset 48)
    MOV     T0, (S10+uintptr(48))           // g0.m = &m0 (offset 48)
    // NOTE: The g0.m offset is 48 on riscv64 — verify against Go source

    // Set up TLS via TP register
    MOV     $runtime·tls_g(SB), T0
    MOV     S10, (T0)                       // Store g0 at tls_g[0]
    ADD     $8, T0                          // TP = &tls_g + 8
    MOV     T0, X4                          // TP register

    // Store exception stack top in SSCRATCH for trap handler
    MOV     $(EXC_STACK_BASE + EXC_STACK_SIZE), T0
    ADD     $KERNEL_VA_OFFSET, T0, T0
    // csrw sscratch, t0  (sscratch = CSR 0x140, t0 = x5)
    WORD    $0x14029073                     // csrw sscratch, t0

    // Call Go DiplomatEntry
    CALL    ·DiplomatEntry(SB)

    // Should not return
halt:
    WORD    $0x10500073                     // wfi
    JMP     halt
```

**IMPORTANT NOTES for the implementer**:
1. The Go assembler for RISC-V is quirky. Register names: A0-A7 (x10-x17), S0-S11 (x8-x9, x18-x27), T0-T6 (x5-x7, x28-x31), SP (x2), TP (x4), ZERO (x0).
2. `MOV` in Go RISC-V assembler is `addi`/`lui+addi` for immediates, `add` for register-to-register.
3. CSR instructions must use WORD encodings because the Go assembler doesn't support them.
4. The g0 struct offsets (0, 8, 16, 24 for stack.lo/hi/guard0/guard1) and m0 struct offset (48 for g0 field) MUST match the Go runtime. Check with `go tool compile -S` if unsure.
5. The exact layout of global variables (savedHartID, etc.) must match the Go declarations.

**CRITICAL**: The assembly above is a STARTING POINT. The exact encodings for CSR instructions and struct offsets need verification. The current `entry_riscv64.s` has working TLS and g0 setup code — adapt that, but remove all UEFI references.

### File: `diplomat/main/entry_globals_riscv64.go` — NEW FILE

```go
package main

// Globals set by assembly entry point
var savedHartID  uint64
var savedFDTAddr uint64
var ptPoolBase   uint64
var bumpAllocStart uint64
var g0StackPA    uint64
var excStackPA   uint64
```

### File: `diplomat/main/main_riscv64.go` — MODIFY

The existing file has `elfMachineExpected = elfMachineRISCV64`. Keep that. Add:

```go
package main

const elfMachineExpected = elfMachineRISCV64

// kernelFilePath is the path to the kernel on the FAT32 disk.
// On RISC-V, files are in the root directory (no /EFI/Linux/ UEFI structure).
const kernelFilePath = "KMAZARIN.ELF"
```

### Test Phase 1

```bash
$GO tool task run-diplomat-riscv64 TIMEOUT=5
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

Expected output: `DP` (D from before MMU, P from after MMU).

If this hangs or crashes: check the QEMU monitor (`echo "info registers" | nc 127.0.0.1 4447`) to see where PC stopped. Common issues:
- Wrong PTE format (check PPN shift)
- Wrong L3 index calculation
- Missing identity map (PC can't fetch instruction after SATP write)

---

## Phase 2: UART Driver + printString in Go

**Goal**: Go code can print strings to serial output.

### File: `diplomat/main/uart_riscv64.go` — NEW FILE

```go
//go:build riscv64

package main

import "unsafe"

const (
    // NS16550A UART virtual address (via linear map)
    uartBaseVA  = 0xFFFFFFFF10000000
    uartTHR     = 0   // Transmit Holding Register
    uartLSR     = 5   // Line Status Register
    uartLSRTHRE = 0x20 // THRE bit
)

// printCharUART writes a single character to the UART.
// This is wired as plat.PrintChar for RISC-V.
//
//go:nosplit
func printCharUART(c uint16) {
    base := uintptr(uartBaseVA)
    // Wait for transmitter ready
    for *(*byte)(unsafe.Pointer(base + uartLSR)) & uartLSRTHRE == 0 {
    }
    *(*byte)(unsafe.Pointer(base + uartTHR)) = byte(c)
}

// debugPortOutUART writes a single byte to the UART.
//
//go:nosplit
func debugPortOutUART(b byte) {
    base := uintptr(uartBaseVA)
    for *(*byte)(unsafe.Pointer(base + uartLSR)) & uartLSRTHRE == 0 {
    }
    *(*byte)(unsafe.Pointer(base + uartTHR)) = b
}
```

### File: `diplomat/main/platform_riscv64.go` — COMPLETE REWRITE

Replace ALL UEFI wrappers with RISC-V implementations.

```go
//go:build riscv64

package main

import "unsafe"

// defaultPlatform wires platform operations for RISC-V.
// No UEFI services — all operations are direct hardware access.
var defaultPlatform = PlatformOps{
    PrintChar:       printCharUART,
    DebugPortOut:    debugPortOutUART,
    AllocatePages:   allocatePhysPagesRISCV,
    ZeroMemory:      zeroMemoryDirect,
    ReadCR3:         readCR3Wrapper,
    WriteCR3:        writeCR3Wrapper,
    HandleProtocol:  nil,  // Not used on RISC-V
    BlockIORead:     nil,  // Not used (VirtIO MMIO instead)
    BlockIOWrite:    nil,
}

var defaultBootSequence = BootSequence{
    InitSpans:          initSpansRISCV,
    GetBlockDevice:     getBlockDeviceRISCV,
    MountFilesystem:    mountFilesystemDefault,
    LoadKernel:         LoadKernel,
    ReadConfig:         readConfigDefault,
    QueryHardware:      queryHardwareRISCV,
    PrepareKernelVM:    PrepareKernelVM,
    InstallFaultHandler: InstallFaultHandler,
    BuildStartupEnv:    BuildStartupEnv,
    JumpToKernelWithEnv: jumpToKernelWithEnvRISCV,
}

var defaultSyscalls = SyscallTable{
    Mmap:   handleMmapSyscall,
    Munmap: handleMunmapSyscall,
    // Other syscalls as needed
}

// readCR3Wrapper reads SATP and returns the L3 root PA.
//
//go:nosplit
func readCR3Wrapper() uint64 {
    satp := readSATP()
    // Extract PPN (bits 43:0) and convert to PA
    ppn := satp & 0x00000FFFFFFFFFFF
    return ppn << 12
}

// writeCR3Wrapper writes SATP with Sv48 mode.
//
//go:nosplit
func writeCR3Wrapper(pa uint64) {
    ppn := pa >> 12
    satp := (uint64(9) << 60) | ppn // MODE=9 (Sv48)
    writeSATP(satp)
}

// zeroMemoryDirect zeros a physical memory region accessed via linear map.
//
//go:nosplit
func zeroMemoryDirect(addr, size uint64) {
    va := uintptr(addr) + 0xFFFFFFFF00000000
    for i := uintptr(0); i < uintptr(size); i += 8 {
        *(*uint64)(unsafe.Pointer(va + i)) = 0
    }
}

// Assembly functions (in pagetable_riscv64.s — already exist and are correct)
func readSATP() uint64
func writeSATP(val uint64)
func flushTLB()
func flushTLBVA(va uint64)
func readTimerCounter() uint64
func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, stvec uint64)

// Forward declarations for TLS operations (in tls_riscv64.s — already exist)
func readTP() uint64
func writeTP(val uint64)

// readBootPageTableBase returns the current SATP root PA.
func readBootPageTableBase() uint64 {
    return readCR3Wrapper()
}
```

### Test Phase 2

After wiring `printCharUART` as `plat.PrintChar`, DiplomatEntry should print:
```
Diplomat UEFI Bootloader
DBG: before InitializeSpans
```

If DiplomatEntry crashes before printing, the issue is likely in g0/TLS setup (Phase 1).

---

## Phase 3: Physical Memory Allocator

**Goal**: Replace UEFI AllocatePages with a bump allocator.

### File: `diplomat/main/bump_alloc_riscv64.go` — NEW FILE

```go
//go:build riscv64

package main

import "unsafe"

const (
    pageSize       = 4096
    // Usable RAM region (after OpenSBI and diplomat)
    // FDT parsing in Phase 4 will refine this.
    // For now, hardcode based on QEMU virt -m 2G:
    //   RAM: 0x80000000 - 0xFFFFFFFF
    //   OpenSBI: 0x80000000 - 0x80200000
    //   Diplomat: loaded around 0x80200000 - ~0x81200000 (assume 16MB max)
    //   PT pool + stacks: 0x81200000 - 0x8121FFFF
    //   Bump allocator: starts at 0x81220000 (set by assembly)
    ramEndDefault = 0x100000000 // 4GB (2GB RAM ends at 0x100000000)
)

// nextFreePage is the bump allocator pointer (physical address).
// Initialized from bumpAllocStart (set by assembly entry).
var nextFreePage uintptr

// ramEnd is the end of usable RAM.
var ramEnd uintptr

// initBumpAllocator sets up the physical page allocator.
// Must be called before any allocatePhysPages calls.
//
//go:nosplit
func initBumpAllocator() {
    nextFreePage = uintptr(bumpAllocStart)
    ramEnd = ramEndDefault
}

// allocatePhysPagesRISCV allocates contiguous physical pages.
// Returns the physical address of the first page, or panics if out of memory.
//
//go:nosplit
func allocatePhysPagesRISCV(count uint64) (uint64, error) {
    pa := nextFreePage
    size := uintptr(count) * pageSize

    if pa + size > ramEnd {
        return 0, &errOutOfMemory
    }

    // Zero the allocated pages (via linear map)
    va := pa + 0xFFFFFFFF00000000
    for i := uintptr(0); i < size; i += 8 {
        *(*uint64)(unsafe.Pointer(va + i)) = 0
    }

    nextFreePage = pa + size
    return uint64(pa), nil
}

var errOutOfMemory = simpleError{"out of physical memory"}

type simpleError struct {
    msg string
}

func (e *simpleError) Error() string { return e.msg }
```

**IMPORTANT**: The `allocatePhysPages` function signature must match what `elf_loader.go` and `kernelvm_riscv64.go` call. Check the existing signature in `pagetable_riscv64.go`:
```go
func allocatePhysPages(count uint64) (uint64, error)
```

Replace the existing `allocatePhysPages` in `pagetable_riscv64.go` with a call to `allocatePhysPagesRISCV`, or rename appropriately.

### File: `diplomat/main/spans_riscv64.go` — NEW FILE

```go
//go:build riscv64

package main

// initSpansRISCV initializes memory span tracking.
// On RISC-V, we use the bump allocator instead of UEFI memory map.
//
//go:nosplit
func initSpansRISCV() bool {
    initBumpAllocator()
    return true
}
```

### Test Phase 3

DiplomatEntry should get past InitSpans and print `DBG: spans OK`.

---

## Phase 4: FDT Parsing

**Goal**: Parse the Flattened Device Tree from OpenSBI to discover RAM and CPU count.

### File: `diplomat/main/fdt_parse_riscv64.go` — NEW FILE

This is a significant piece of code (~200-300 lines). The FDT is a binary format with:
- Header (40 bytes): magic, totalsize, struct offset, strings offset
- Structure block: FDT_BEGIN_NODE(1), FDT_END_NODE(2), FDT_PROP(3), FDT_NOP(4), FDT_END(9)
- Strings block: null-terminated property names

```go
//go:build riscv64

package main

import "unsafe"

// FDT magic number
const fdtMagic = 0xD00DFEED

// FDT tokens
const (
    fdtBeginNode = 1
    fdtEndNode   = 2
    fdtProp      = 3
    fdtNOP       = 4
    fdtEnd       = 9
)

// FDTHeader is the Flattened Device Tree header (big-endian)
type FDTHeader struct {
    Magic           uint32
    TotalSize       uint32
    OffDtStruct     uint32
    OffDtStrings    uint32
    OffMemRsvmap    uint32
    Version         uint32
    LastCompVersion uint32
    BootCpuidPhys   uint32
    SizeDtStrings   uint32
    SizeDtStruct    uint32
}

// FDTInfo holds the parsed FDT data needed by diplomat.
type FDTInfo struct {
    RAMBase   uint64
    RAMSize   uint64
    CPUCount  uint64
    // Reserved memory regions (OpenSBI)
    ReservedBase [4]uint64
    ReservedSize [4]uint64
    NumReserved  int
}

// be32 reads a big-endian uint32 from memory.
//
//go:nosplit
func be32(addr uintptr) uint32 {
    b := (*[4]byte)(unsafe.Pointer(addr))
    return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// be64 reads a big-endian uint64 from memory.
//
//go:nosplit
func be64(addr uintptr) uint64 {
    return uint64(be32(addr))<<32 | uint64(be32(addr+4))
}

// parseFDT parses the Flattened Device Tree to extract RAM and CPU info.
// fdtPA is the physical address of the FDT (from OpenSBI A1 register).
func parseFDT(fdtPA uint64) *FDTInfo {
    info := dNew[FDTInfo]()
    if info == nil {
        return nil
    }

    // Access FDT via linear map
    fdtVA := uintptr(fdtPA) + 0xFFFFFFFF00000000

    // Validate header
    magic := be32(fdtVA)
    if magic != fdtMagic {
        printString("FDT: bad magic\r\n")
        return nil
    }

    totalSize := be32(fdtVA + 4)
    structOff := be32(fdtVA + 8)
    stringsOff := be32(fdtVA + 12)

    structBase := fdtVA + uintptr(structOff)
    stringsBase := fdtVA + uintptr(stringsOff)

    printString("FDT: size=")
    printHex(uint64(totalSize))
    printString(" struct@")
    printHex(uint64(structOff))
    printString(" strings@")
    printHex(uint64(stringsOff))
    printString("\r\n")

    // Walk the structure block
    pos := structBase
    endPos := structBase + uintptr(totalSize) // safety limit
    depth := 0
    inMemory := false
    inCPUs := false
    inReservedMemory := false

    for pos < endPos {
        token := be32(pos)
        pos += 4

        switch token {
        case fdtBeginNode:
            // Node name follows (null-terminated, padded to 4 bytes)
            nameStart := pos
            nameLen := 0
            for *(*byte)(unsafe.Pointer(pos + uintptr(nameLen))) != 0 {
                nameLen++
                if nameLen > 128 {
                    break
                }
            }
            // Advance past name + null + padding
            pos += uintptr((nameLen + 4) & ^3)

            depth++

            // Check node names we care about
            if depth == 1 {
                if matchNodeName(nameStart, "memory", 6) {
                    inMemory = true
                } else if matchNodeName(nameStart, "cpus", 4) {
                    inCPUs = true
                } else if matchNodeName(nameStart, "reserved-memory", 15) {
                    inReservedMemory = true
                }
            }
            if depth == 2 && inCPUs {
                if matchNodePrefix(nameStart, "cpu@", 4) {
                    info.CPUCount++
                }
            }
            if depth == 2 && inReservedMemory {
                // Will parse reg property for reserved ranges
            }

        case fdtEndNode:
            if depth == 1 {
                inMemory = false
                inCPUs = false
                inReservedMemory = false
            }
            depth--

        case fdtProp:
            // Property: len(4) + nameoff(4) + data(len, padded to 4)
            propLen := be32(pos)
            nameOff := be32(pos + 4)
            pos += 8
            dataStart := pos
            pos += uintptr((propLen + 3) & ^uint32(3))

            // Get property name from strings block
            propName := stringsBase + uintptr(nameOff)

            if inMemory && depth == 1 {
                if matchPropName(propName, "reg") {
                    // /memory reg: pairs of (base, size) as 64-bit BE values
                    if propLen >= 16 {
                        info.RAMBase = be64(dataStart)
                        info.RAMSize = be64(dataStart + 8)
                    }
                }
            }

        case fdtNOP:
            // Skip

        case fdtEnd:
            // Done
            goto done

        default:
            // Unknown token, skip
            break
        }
    }

done:
    // Default if FDT didn't have /memory
    if info.RAMSize == 0 {
        info.RAMBase = 0x80000000
        info.RAMSize = 0x80000000 // 2GB default
    }
    if info.CPUCount == 0 {
        info.CPUCount = 1
    }

    printString("FDT: RAM=")
    printHex(info.RAMBase)
    printString("-")
    printHex(info.RAMBase + info.RAMSize)
    printString(" CPUs=")
    printHex(info.CPUCount)
    printString("\r\n")

    return info
}

// matchNodeName checks if node name matches expected (exact match or with @suffix)
//
//go:nosplit
func matchNodeName(nameAddr uintptr, expected string, expLen int) bool {
    for i := 0; i < expLen; i++ {
        c := *(*byte)(unsafe.Pointer(nameAddr + uintptr(i)))
        if c != expected[i] {
            return false
        }
    }
    // Must end with NUL or '@'
    next := *(*byte)(unsafe.Pointer(nameAddr + uintptr(expLen)))
    return next == 0 || next == '@'
}

// matchNodePrefix checks if node name starts with prefix
//
//go:nosplit
func matchNodePrefix(nameAddr uintptr, prefix string, prefLen int) bool {
    for i := 0; i < prefLen; i++ {
        c := *(*byte)(unsafe.Pointer(nameAddr + uintptr(i)))
        if c != prefix[i] {
            return false
        }
    }
    return true
}

// matchPropName checks if a property name matches
//
//go:nosplit
func matchPropName(nameAddr uintptr, expected string) bool {
    for i := 0; i < len(expected); i++ {
        c := *(*byte)(unsafe.Pointer(nameAddr + uintptr(i)))
        if c != expected[i] {
            return false
        }
    }
    return *(*byte)(unsafe.Pointer(nameAddr + uintptr(len(expected)))) == 0
}
```

### File: `diplomat/main/hardware_riscv64.go` — REWRITE

```go
//go:build riscv64

package main

// QueryHardware discovers CPU count and RAM from the FDT.
func queryHardwareRISCV(config *Config) (*HardwareInfo, error) {
    info := parseFDT(savedFDTAddr)
    if info == nil {
        return nil, &simpleError{"failed to parse FDT"}
    }

    hw := dNew[HardwareInfo]()
    if hw == nil {
        return nil, &simpleError{"allocation failed"}
    }

    hw.CPUCount = info.CPUCount
    hw.RAMBase = info.RAMBase
    hw.RAMSize = info.RAMSize

    // Update bump allocator with actual RAM end
    ramEnd = uintptr(info.RAMBase + info.RAMSize)

    return hw, nil
}
```

### Test Phase 4

Expected output includes FDT parsing results:
```
FDT: RAM=0x80000000-0x100000000 CPUs=1
Hardware: 1 CPUs, 2048MB RAM @ 0x80000000
```

---

## Phase 5: VirtIO MMIO Block Device + FAT32

**Goal**: Diplomat reads kmazarin.elf from the VirtIO block disk.

### Step 5a: Enable MMIO VirtIO block driver for RISC-V

The existing `shared/bootloader/virtio_block.go` is ARM64-only. Change the build tag.

### File: `shared/bootloader/virtio_block.go` — MODIFY BUILD TAG

Change line 9:
```go
// OLD:
//go:build arm64

// NEW:
//go:build arm64 || riscv64
```

### Step 5b: VirtIO MMIO block device discovery

### File: `diplomat/main/blockdev_riscv64.go` — NEW FILE

```go
//go:build riscv64

package main

import (
    "mazzy/shared/blockdev"
    "mazzy/shared/bootloader"
)

// VirtIO MMIO device addresses on QEMU virt platform.
// QEMU creates 8 VirtIO MMIO devices at these addresses.
// The first one that has a VirtIO block device attached is our disk.
var virtioMMIOAddrs = [8]uintptr{
    0x10008000, // highest priority (QEMU assigns in reverse)
    0x10007000,
    0x10006000,
    0x10005000,
    0x10004000,
    0x10003000,
    0x10002000,
    0x10001000,
}

// getBlockDeviceRISCV finds and initializes the VirtIO MMIO block device.
func getBlockDeviceRISCV() (blockdev.BlockDevice, error) {
    // Access MMIO addresses via linear map
    const linearMapOffset = 0xFFFFFFFF00000000

    for _, pa := range virtioMMIOAddrs {
        va := pa + linearMapOffset
        dev, err := bootloader.InitVirtIOBlock(va)
        if err == nil {
            printString("VirtIO block device found at PA ")
            printHex(uint64(pa))
            printString("\r\n")
            return dev, nil
        }
    }

    return nil, &simpleError{"no VirtIO block device found"}
}
```

**IMPORTANT**: The `InitVirtIOBlock` function accesses MMIO registers directly via
pointer dereference. After the MMU is enabled, the linear map
(VA = PA + 0xFFFFFFFF00000000) covers these addresses.

### Step 5c: Modify Taskfile QEMU command

### File: `Taskfile.yml` — MODIFY run-diplomat-riscv64-background

Change `-device virtio-blk-pci` to `-device virtio-blk-device`:

```yaml
# In run-diplomat-riscv64-background, change the QEMU line:
# OLD:
#   -device virtio-blk-pci,drive=drive-virtio-disk0
# NEW:
#   -device virtio-blk-device,drive=drive-virtio-disk0
```

This uses MMIO transport instead of PCI, which is much simpler for diplomat
(no PCI ECAM scanning, BAR mapping, or VirtIO PCI capability parsing needed).

### Step 5d: Fix kernel file path for RISC-V

The current `findFile` function hardcodes the path `/EFI/Linux/kmazarin.elf` which
is a UEFI convention. On RISC-V, the FAT32 disk has files in the root directory.

### File: `diplomat/main/elf_loader.go` — MODIFY findFile

The `findFile` function needs to handle the RISC-V case where kmazarin.elf is in
the root directory. Two approaches:

**Option A** (recommended): Add an arch-specific `findKernelFile` function.

Add to `diplomat/main/elf_loader_riscv64.go` — NEW FILE:
```go
//go:build riscv64

package main

import "mazzy/shared/fs/fat32"

// findKernelFile finds kmazarin.elf in the root directory.
func findKernelFile(fs *fat32.FileSystem) (*SimpleFile, error) {
    cluster := fs.RootCluster()
    entry, err := findInDir(fs, cluster, "KMAZARIN-RISCV64.ELF")
    if err != nil {
        // Also try KMAZARIN.ELF
        entry, err = findInDir(fs, cluster, "KMAZARIN.ELF")
        if err != nil {
            return nil, err
        }
    }

    sf := dNew[SimpleFile]()
    if sf == nil {
        return nil, &errDNewSimpleFileFailed
    }
    sf.Cluster = entry.Cluster
    sf.Size = entry.Size
    return sf, nil
}
```

And for non-RISC-V, add `diplomat/main/elf_loader_uefi.go`:
```go
//go:build !riscv64

package main

import "mazzy/shared/fs/fat32"

func findKernelFile(fs *fat32.FileSystem) (*SimpleFile, error) {
    return findFile(fs, "/EFI/Linux/kmazarin.elf")
}
```

Then modify `LoadKernel` to call `findKernelFile(fsys)` instead of `findFile(fsys, path)`.

### Step 5e: Disk image with correct filename

Modify the `disk-riscv64` task to copy kmazarin with a known name:

### File: `Taskfile.yml` — MODIFY disk-riscv64

Add a step to copy kmazarin.elf to the staging directory with a predictable name:
```yaml
# Add before mkfat32:
- '{{.GO}} tool go-cp {{.KMAZARIN_RISCV64_ELF}} {{.BUILD_DIR}}/disk-staging-riscv64/kmazarin.elf'
# Then use the staging copy in mkfat32:
- '{{.GO}} tool mkfat32 -o {{.DISK_RISCV64_IMAGE}} {{.BUILD_DIR}}/disk-staging-riscv64/dapope.elf {{.BUILD_DIR}}/disk-staging-riscv64/stdio.elf {{.BUILD_DIR}}/disk-staging-riscv64/kmazarin.elf {{.BUILD_DIR}}/boot-image.bin'
```

### Test Phase 5

Expected output:
```
VirtIO block device found at PA 0x10008000
Mounting FAT32...
FAT32 mounted OK
Kernel file found
ELF: entry=...
Kernel loaded OK
```

---

## Phase 6: Kernel VM Setup + Demand Paging

**Goal**: Build complete Sv48 page tables for kmazarin, install demand paging handler.

### File: `diplomat/main/kernelvm_riscv64.go` — MAJOR REWRITE

The current file builds Sv39 tables and uses UEFI AllocatePages. Rewrite for Sv48
using the bump allocator. The structure largely follows the existing code but:
1. Change from Sv39 (3-level) to Sv48 (4-level)
2. Replace `allocatePhysPages` UEFI calls with bump allocator
3. Change address calculations for Sv48

Key constants to update:
```go
const (
    LinearMapMaxPA  = 0x100000000 // 4GB
    KernelVAOffset  = 0xFFFFFFFF00000000

    // Stack virtual addresses (same as ARM64/x86_64)
    KernelG0StackBottom = 0xFFFFFFFF43E00000
    KernelG0StackSize   = 32 * 1024
    KernelG0StackTop    = KernelG0StackBottom + KernelG0StackSize
    KernelExcStackBottom = KernelG0StackTop
    KernelExcStackSize   = 16 * 1024
    KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize
)
```

The `PrepareKernelVM` function must:
1. Allocate a NEW L3 root page table (separate from diplomat's boot tables)
2. Map the linear map (4 x 1GB gigapages via L2 leaf entries)
3. Map kmazarin's code/data at high VA (2MB leaf entries)
4. Map g0 stack and exception stack (4KB entries)
5. Allocate heap demand-paging pool
6. Allocate unified pool for kmazarin
7. Map MMIO regions (UART, PLIC, CLINT, VirtIO)

**CRITICAL**: The Sv48 PTE format is identical to what's already in
`pagetable_riscv64.go` (PPN in bits 53:10, flags in bits 9:0). The `makePTE`
function is correct. The L2/L1/L0 index functions need to be updated for Sv48
(add L3 index for bits 47:39).

### File: `diplomat/main/pagetable_riscv64.go` — REWRITE for Sv48

Add L3 index function and update BuildPageTables for 4-level Sv48:

```go
// l3Index returns the L3 (root) page table index for a virtual address.
// Sv48: VA[47:39], 9 bits
//
//go:nosplit
func l3Index(va uint64) int {
    return int((va >> 39) & 0x1FF)
}
```

The existing `l2Index`, `l1Index`, `l0Index` functions are already correct for Sv48
(same shift values as ARM64/x86_64).

### File: `diplomat/main/exc_vectors_riscv64.s` — REWRITE

Replace the WFI stub with a real demand paging handler:

```asm
#include "textflag.h"

// diplomatExceptionHandler is the S-mode trap handler.
// Called when SCAUSE indicates a page fault during kmazarin boot.
//
// Entry state:
//   SSCRATCH = exception stack top (VA)
//   SP = thread stack (may be the faulting stack)
//   SCAUSE = trap cause
//   STVAL = faulting address
//   SEPC = faulting PC
//
// We handle:
//   SCAUSE 13 (load page fault) — demand paging
//   SCAUSE 15 (store page fault) — demand paging
//   SCAUSE 12 (instruction page fault) — demand paging
//
// All other causes: print diagnostic and halt.

TEXT ·diplomatExceptionHandler(SB), NOSPLIT|NOFRAME, $0
    // Swap SP with SSCRATCH (switch to exception stack)
    // csrrw sp, sscratch, sp
    WORD    $0x14011173     // csrrw sp, sscratch, sp (x2 <-> sscratch)

    // Save caller-saved registers
    ADD     $-256, SP
    MOV     RA, 0(SP)
    MOV     T0, 8(SP)
    MOV     T1, 16(SP)
    MOV     T2, 24(SP)
    MOV     A0, 32(SP)
    MOV     A1, 40(SP)
    MOV     A2, 48(SP)
    MOV     A3, 56(SP)
    MOV     A4, 64(SP)
    MOV     A5, 72(SP)
    MOV     A6, 80(SP)
    MOV     A7, 88(SP)
    MOV     T3, 96(SP)
    MOV     T4, 104(SP)
    MOV     T5, 112(SP)
    MOV     T6, 120(SP)
    MOV     S0, 128(SP)
    MOV     S1, 136(SP)

    // Read SCAUSE
    // csrr a0, scause (scause = 0x142)
    WORD    $0x14202573     // csrr a0, scause

    // Check if it's a page fault (12, 13, or 15)
    MOV     $12, T0
    BEQ     A0, T0, handle_page_fault
    MOV     $13, T0
    BEQ     A0, T0, handle_page_fault
    MOV     $15, T0
    BEQ     A0, T0, handle_page_fault

    // Unknown exception — call Go handler with scause in A0
    // csrr a1, stval (stval = 0x143)
    WORD    $0x14302573+0x00000800   // WRONG — need correct encoding
    // Actually: csrr a1, stval
    // a1=x11, stval=0x143
    // csrrs x11, 0x143, x0
    // imm=0x143, rs1=x0, funct3=010, rd=x11, opcode=1110011
    WORD    $0x14305573     // csrr a1, stval (reading into a1)
    CALL    ·handleUnknownException(SB)
    JMP     halt_exception

handle_page_fault:
    // Read STVAL (faulting address)
    // csrr a0, stval
    WORD    $0x14302573     // csrr a0, stval
    // Call Go demand paging handler
    CALL    ·handleDemandPageFault(SB)

    // Restore registers
    MOV     0(SP), RA
    MOV     8(SP), T0
    MOV     16(SP), T1
    MOV     24(SP), T2
    MOV     32(SP), A0
    MOV     40(SP), A1
    MOV     48(SP), A2
    MOV     56(SP), A3
    MOV     64(SP), A4
    MOV     72(SP), A5
    MOV     80(SP), A6
    MOV     88(SP), A7
    MOV     96(SP), T3
    MOV     104(SP), T4
    MOV     112(SP), T5
    MOV     120(SP), T6
    MOV     128(SP), S0
    MOV     136(SP), S1
    ADD     $256, SP

    // Restore original SP from SSCRATCH
    // csrrw sp, sscratch, sp
    WORD    $0x14011173

    // Return from exception
    // sret
    WORD    $0x10200073

halt_exception:
    WORD    $0x10500073     // wfi
    JMP     halt_exception

// getDiplomatExceptionHandlerAddr returns the address of the handler.
TEXT ·getDiplomatExceptionHandlerAddr(SB), NOSPLIT, $0-8
    MOV     $·diplomatExceptionHandler(SB), A0
    MOV     A0, ret+0(FP)
    RET

// setSTVEC sets the STVEC CSR to the given address (Direct mode).
TEXT ·setSTVEC(SB), NOSPLIT, $0-8
    MOV     addr+0(FP), A0
    AND     $-4, A0, A0     // Clear low 2 bits (Direct mode)
    // csrw stvec, a0 (stvec = 0x105)
    WORD    $0x10551073     // csrw stvec, a0
    RET
```

**IMPORTANT**: The WORD encodings for CSR instructions need careful verification.
The general encoding for `csrr rd, csr` is `csrrs rd, csr, x0`:
- `0x{csr[11:0]}02{rd_encoding}73` approximately

Look at the existing `pagetable_riscv64.s` for reference encodings that are KNOWN CORRECT:
- `csrr a0, satp` = `0x180022F3` (satp=0x180, a0=x10, rd field)
- `csrw satp, a0` = `0x18051073` (satp=0x180, a0=x10, rs1 field)

### File: `diplomat/main/demand_page_riscv64.go` — NEW FILE

```go
//go:build riscv64

package main

import "unsafe"

// Demand paging pool (pre-allocated pages for page fault handling)
var demandPagePool struct {
    pages    [4096]uint64 // Physical addresses of available pages
    count    int
    used     int
    // PT pages for page table allocation during faults
    ptPages  [256]uint64
    ptCount  int
    ptUsed   int
}

// initDemandPagePool pre-allocates pages for demand paging.
func initDemandPagePool(numPages, numPTPages int) {
    for i := 0; i < numPages && i < len(demandPagePool.pages); i++ {
        pa, err := allocatePhysPagesRISCV(1)
        if err != nil {
            break
        }
        demandPagePool.pages[i] = pa
        demandPagePool.count++
    }

    for i := 0; i < numPTPages && i < len(demandPagePool.ptPages); i++ {
        pa, err := allocatePhysPagesRISCV(1)
        if err != nil {
            break
        }
        demandPagePool.ptPages[i] = pa
        demandPagePool.ptCount++
    }

    printString("Demand page pool: ")
    printHex(uint64(demandPagePool.count))
    printString(" data + ")
    printHex(uint64(demandPagePool.ptCount))
    printString(" PT pages\r\n")
}

// allocDemandPage returns a physical page from the pre-allocated pool.
//
//go:nosplit
func allocDemandPage() uint64 {
    if demandPagePool.used >= demandPagePool.count {
        return 0
    }
    pa := demandPagePool.pages[demandPagePool.used]
    demandPagePool.used++
    return pa
}

// allocDemandPTPage returns a page table page from the pool.
//
//go:nosplit
func allocDemandPTPage() uint64 {
    if demandPagePool.ptUsed >= demandPagePool.ptCount {
        return 0
    }
    pa := demandPagePool.ptPages[demandPagePool.ptUsed]
    demandPagePool.ptUsed++
    return pa
}

// handleDemandPageFault handles a page fault during kmazarin boot.
// Called from assembly with the faulting address in A0.
//
//go:nosplit
func handleDemandPageFault(faultAddr uint64) {
    // Only handle faults in the heap VA range
    if faultAddr < KernelHeapStart || faultAddr >= KernelHeapEnd {
        debugPortOutUART('!')
        printString("PAGE FAULT outside heap: ")
        printHex(faultAddr)
        printString("\r\n")
        for {
        }
    }

    // Allocate a physical page
    pa := allocDemandPage()
    if pa == 0 {
        printString("DEMAND PAGE POOL EXHAUSTED\r\n")
        for {
        }
    }

    // Zero the page via linear map
    va := uintptr(pa) + 0xFFFFFFFF00000000
    for i := uintptr(0); i < 4096; i += 8 {
        *(*uint64)(unsafe.Pointer(va + i)) = 0
    }

    // Map the page in the kernel page tables
    // Walk L3→L2→L1→L0, allocating intermediate tables as needed
    mapDemandPage(faultAddr, pa)

    // Flush TLB for this VA
    flushTLBVA(faultAddr)
}

// mapDemandPage inserts a 4KB leaf PTE for the given VA→PA mapping.
//
//go:nosplit
func mapDemandPage(va, pa uint64) {
    // Get kernel L3 root from SATP
    rootPA := readCR3Wrapper()
    rootVA := rootPA + 0xFFFFFFFF00000000

    l3Idx := (va >> 39) & 0x1FF
    l2Idx := (va >> 30) & 0x1FF
    l1Idx := (va >> 21) & 0x1FF
    l0Idx := (va >> 12) & 0x1FF

    // Walk or create L2 table
    l3EntryAddr := rootVA + l3Idx*8
    l3Entry := *(*uint64)(unsafe.Pointer(uintptr(l3EntryAddr)))
    if l3Entry&0x1 == 0 {
        // Allocate L2 table
        l2PA := allocDemandPTPage()
        if l2PA == 0 {
            return
        }
        ppn := l2PA >> 12
        *(*uint64)(unsafe.Pointer(uintptr(l3EntryAddr))) = (ppn << 10) | 0x1
        l3Entry = (ppn << 10) | 0x1
    }

    l2PA := ((l3Entry >> 10) & 0xFFFFFFFFFFF) << 12
    l2VA := l2PA + 0xFFFFFFFF00000000
    l2EntryAddr := l2VA + l2Idx*8
    l2Entry := *(*uint64)(unsafe.Pointer(uintptr(l2EntryAddr)))
    if l2Entry&0x1 == 0 {
        l1PA := allocDemandPTPage()
        if l1PA == 0 {
            return
        }
        ppn := l1PA >> 12
        *(*uint64)(unsafe.Pointer(uintptr(l2EntryAddr))) = (ppn << 10) | 0x1
        l2Entry = (ppn << 10) | 0x1
    }

    l1PA := ((l2Entry >> 10) & 0xFFFFFFFFFFF) << 12
    l1VA := l1PA + 0xFFFFFFFF00000000
    l1EntryAddr := l1VA + l1Idx*8
    l1Entry := *(*uint64)(unsafe.Pointer(uintptr(l1EntryAddr)))
    if l1Entry&0x1 == 0 {
        l0PA := allocDemandPTPage()
        if l0PA == 0 {
            return
        }
        ppn := l0PA >> 12
        *(*uint64)(unsafe.Pointer(uintptr(l1EntryAddr))) = (ppn << 10) | 0x1
        l1Entry = (ppn << 10) | 0x1
    }

    l0PA := ((l1Entry >> 10) & 0xFFFFFFFFFFF) << 12
    l0VA := l0PA + 0xFFFFFFFF00000000
    l0EntryAddr := l0VA + l0Idx*8

    // Create leaf PTE: V + R + W + A + D (kernel heap page)
    ppn := pa >> 12
    pte := (ppn << 10) | 0xC7 // V|R|W|A|D = 0x01|0x02|0x04|0x40|0x80 = 0xC7
    *(*uint64)(unsafe.Pointer(uintptr(l0EntryAddr))) = pte
}

// handleUnknownException is called for non-page-fault exceptions.
//
//go:nosplit
func handleUnknownException(scause, stval uint64) {
    printString("\r\nFATAL EXCEPTION: scause=")
    printHex(scause)
    printString(" stval=")
    printHex(stval)
    printString("\r\n")
}
```

### Test Phase 6

After `PrepareKernelVM` and `InstallFaultHandler`, the kernel VM is ready.
The startup env (auxv) is built and diplomat should print:
```
Preparing kernel VM...
Demand page pool: 0x1000 data + 0x100 PT pages
Startup env at VA 0xFFFFFFFF43E07D00 (phys ...)
Jumping to kmazarin...
```

---

## Phase 7: Jump to Kmazarin + Runtime

**Goal**: Diplomat jumps to kmazarin entry point. Kmazarin boots.

### File: `diplomat/main/kernelvm_riscv64.go` — jumpToKernelWithEnvRISCV

```go
// jumpToKernelWithEnvRISCV jumps to kmazarin with proper stack and exception handler.
func jumpToKernelWithEnvRISCV(entry, stackPtr, excStackTop, excVecAddr uint64) {
    printString("Jump: entry=")
    printHex(entry)
    printString(" sp=")
    printHex(stackPtr)
    printString(" exc=")
    printHex(excStackTop)
    printString(" stvec=")
    printHex(excVecAddr)
    printString("\r\n")

    // jumpToKmazarinWithStack sets SP, SSCRATCH, STVEC, clears regs, and jumps
    // (already implemented correctly in pagetable_riscv64.s)
    jumpToKmazarinWithStack(entry, stackPtr, excStackTop, excVecAddr)
}
```

### File: `diplomat/main/startup_env.go` — MODIFY for RISC-V

The `findDTBFromUEFI` function searches the UEFI config table. On RISC-V, we have
the FDT address directly from OpenSBI. Modify the DTB lookup:

Add to `diplomat/main/startup_env_riscv64.go` — NEW FILE:
```go
//go:build riscv64

package main

// findDTBForPlatform returns the FDT address.
// On RISC-V, this comes directly from OpenSBI (A1 register at entry).
func findDTBForPlatform() uint64 {
    return savedFDTAddr
}
```

Add to `diplomat/main/startup_env_uefi.go` — NEW FILE:
```go
//go:build !riscv64

package main

// findDTBForPlatform returns the DTB address.
// On UEFI platforms, search the config table, falling back to synthetic DTB.
func findDTBForPlatform(hw *HardwareInfo) uint64 {
    addr := findDTBFromUEFI()
    if addr == 0 {
        addr = buildSyntheticDTB(hw)
    }
    return addr
}
```

Then in `startup_env.go`, replace the DTB lookup code block with:
```go
// Get DTB address (platform-specific)
dtbAddr := findDTBForPlatform(hw)
```

Wait — the function signatures differ. The RISC-V version doesn't need `hw`.
Use a simpler approach: just override `buildSyntheticDTB` and `findDTBFromUEFI`
on RISC-V to return the saved FDT address.

Actually, the simplest fix: In `startup_env.go`, the DTB code is:
```go
dtbAddr := findDTBFromUEFI()
if dtbAddr == 0 {
    dtbAddr = buildSyntheticDTB(hw)
}
```

On RISC-V, `findDTBFromUEFI` will fail (systemTable is nil). Then `buildSyntheticDTB`
will run. We should instead provide the REAL FDT.

**Fix**: Override `buildSyntheticDTB` on RISC-V to return `savedFDTAddr`:

Add to `diplomat/main/dtb_riscv64.go` — REWRITE:
```go
//go:build riscv64

package main

// buildSyntheticDTB on RISC-V returns the real FDT from OpenSBI.
// No synthetic DTB needed — OpenSBI provides a complete, accurate FDT.
func buildSyntheticDTB(hw *HardwareInfo) uint64 {
    return savedFDTAddr
}
```

### File: `shared/constants/addresses_riscv64.go` — UPDATE for Sv48

The current file has Sv39 addresses. Update for Sv48:

```go
package constants

const (
    // RISC-V Sv48 kernel heap addresses.
    // Sv48 supports 48-bit VAs with canonical addressing:
    //   Lower half (user): 0x0000000000000000 - 0x00007FFFFFFFFFFF
    //   Upper half (kernel): 0xFFFF800000000000 - 0xFFFFFFFFFFFFFFFF
    //
    // The linear map is at 0xFFFFFFFF00000000 (diplomat's KernelVAOffset).
    // Heap is placed well below the linear map in kernel space.
    KernelHeapStart = 0xFFFF000100000000
    KernelHeapEnd   = 0xFFFF001000000000 // 64GB range
    KernelHeapSize  = KernelHeapEnd - KernelHeapStart
)
```

**NOTE**: These must match what kmazarin expects. Check that kmazarin's
`paging_riscv64.go` heap fault handler covers this range.

### Files to DELETE

These files contain UEFI-specific code that is not needed on RISC-V:

1. `diplomat/main/uefi_calls_riscv64.s` — UEFI assembly wrappers (DELETE)
2. `diplomat/main/uefi_mem_riscv64.go` — UEFI memory allocation wrappers (DELETE)

### Test Phase 7

Expected output (kmazarin boots):
```
Jumping to kmazarin...
[Main] Kmazarin kernel starting...
[Main] Processing auxv...
```

If kmazarin crashes immediately after jump:
- Check that STVEC is set to kmazarin's ExceptionVectorTable
- Check that the auxv entries are correct (especially heap addresses)
- Check that the page table root PA is correct in AT_TTBR1_L0_PHYS

---

## Files Summary

### New Files to Create
| File | Phase | Purpose |
|------|-------|---------|
| `diplomat/main/entry_globals_riscv64.go` | 1 | Global variables set by assembly |
| `diplomat/main/uart_riscv64.go` | 2 | NS16550A UART driver |
| `diplomat/main/bump_alloc_riscv64.go` | 3 | Physical page bump allocator |
| `diplomat/main/spans_riscv64.go` | 3 | InitSpans for RISC-V |
| `diplomat/main/fdt_parse_riscv64.go` | 4 | FDT parser |
| `diplomat/main/blockdev_riscv64.go` | 5 | VirtIO MMIO block device |
| `diplomat/main/elf_loader_riscv64.go` | 5 | RISC-V kernel file finder |
| `diplomat/main/elf_loader_uefi.go` | 5 | UEFI kernel file finder |
| `diplomat/main/demand_page_riscv64.go` | 6 | Demand paging handler |
| `diplomat/main/startup_env_riscv64.go` | 7 | DTB override for startup env |

### Files to Rewrite
| File | Phase | Changes |
|------|-------|---------|
| `diplomat/main/entry_riscv64.s` | 1 | Complete rewrite: OpenSBI entry, Sv48 tables, MMU enable |
| `diplomat/main/platform_riscv64.go` | 2 | Replace UEFI ops with UART/bump alloc |
| `diplomat/main/hardware_riscv64.go` | 4 | FDT-based hardware discovery |
| `diplomat/main/kernelvm_riscv64.go` | 6 | Sv48 kernel VM, bump allocator |
| `diplomat/main/pagetable_riscv64.go` | 6 | Sv48 4-level page tables |
| `diplomat/main/exc_vectors_riscv64.s` | 6 | Demand paging exception handler |
| `diplomat/main/dtb_riscv64.go` | 7 | Return real FDT (not synthetic) |

### Files to Modify
| File | Phase | Changes |
|------|-------|---------|
| `shared/bootloader/virtio_block.go` | 5 | Change build tag to `arm64 \|\| riscv64` |
| `shared/constants/addresses_riscv64.go` | 7 | Update to Sv48 heap addresses |
| `diplomat/main/elf_loader.go` | 5 | Use `findKernelFile()` instead of `findFile()` |
| `Taskfile.yml` | 5 | MMIO block device, disk staging |

### Files to Delete
| File | Reason |
|------|--------|
| `diplomat/main/uefi_calls_riscv64.s` | UEFI assembly not needed |
| `diplomat/main/uefi_mem_riscv64.go` | UEFI memory not needed |

### Files to Keep Unchanged
| File | Reason |
|------|--------|
| `diplomat/main/pagetable_riscv64.s` | CSR operations correct (readSATP, writeSATP, flushTLB, jumpToKmazarinWithStack) |
| `diplomat/main/tls_riscv64.s` | readTP/writeTP correct |
| `diplomat/main/dtb_hwcap_riscv64.go` | HWCAP computation correct |
| `diplomat/main/dmalloc.go` | Bump allocator for Go objects (shared) |

---

## Debugging Tips

1. **Print characters from assembly**: Write directly to UART at PA `0x10000000`
   (before MMU) or VA `0xFFFFFFFF10000000` (after MMU). Use `MOVB` to write.

2. **QEMU monitor**: `echo "info registers" | nc 127.0.0.1 4447` shows all
   register values including PC, SP, SATP, SCAUSE, STVAL, SEPC.

3. **Page fault debugging**: If you get a fault, check:
   - SCAUSE: 12=inst fetch, 13=load, 15=store page fault
   - STVAL: the faulting virtual address
   - SEPC: the instruction that caused the fault

4. **Common mistakes**:
   - Forgetting the identity map (crash right after `csrw satp`)
   - Wrong PPN shift (must be >>12 then <<10 in PTE, not >>2)
   - Missing A and D bits on leaf PTEs (hardware may not set automatically)
   - Accessing globals before TLS is set up (g register is nil)
   - Not flushing TLB after page table modifications

5. **Go assembler quirks**:
   - `MOV $imm, reg` generates `LUI + ADDI` for large immediates
   - CSR instructions MUST use WORD encodings
   - S2-S11 may not be recognized — use WORD if needed
   - `X4` is the TP register (TLS pointer)
   - `S10` (X26) is Go's g register on RISC-V

6. **Build errors**: If you get "undefined: xyz" errors, check build tags
   (`//go:build riscv64`). Make sure new files have the right tag.

---

## Implementation Order Checklist

- [ ] Phase 1: Assembly entry, UART, Sv48 page tables, MMU enable
  - [ ] Rewrite `entry_riscv64.s`
  - [ ] Create `entry_globals_riscv64.go`
  - [ ] Test: "DP" appears in serial log
- [ ] Phase 2: Go UART driver, platform wiring
  - [ ] Create `uart_riscv64.go`
  - [ ] Rewrite `platform_riscv64.go`
  - [ ] Test: Go prints to serial
- [ ] Phase 3: Bump allocator
  - [ ] Create `bump_alloc_riscv64.go`
  - [ ] Create `spans_riscv64.go`
  - [ ] Test: passes InitSpans
- [ ] Phase 4: FDT parsing
  - [ ] Create `fdt_parse_riscv64.go`
  - [ ] Rewrite `hardware_riscv64.go`
  - [ ] Test: RAM/CPU discovered from FDT
- [ ] Phase 5: VirtIO block + FAT32
  - [ ] Modify `shared/bootloader/virtio_block.go` build tag
  - [ ] Create `blockdev_riscv64.go`
  - [ ] Create `elf_loader_riscv64.go` + `elf_loader_uefi.go`
  - [ ] Modify `elf_loader.go` to use `findKernelFile()`
  - [ ] Modify `Taskfile.yml` (MMIO device, disk staging)
  - [ ] Test: kmazarin loaded from disk
- [ ] Phase 6: Kernel VM + demand paging
  - [ ] Rewrite `kernelvm_riscv64.go` for Sv48
  - [ ] Rewrite `pagetable_riscv64.go` for Sv48
  - [ ] Rewrite `exc_vectors_riscv64.s`
  - [ ] Create `demand_page_riscv64.go`
  - [ ] Test: demand page pool allocated
- [ ] Phase 7: Jump to kmazarin
  - [ ] Add `jumpToKernelWithEnvRISCV` to kernelvm
  - [ ] Rewrite `dtb_riscv64.go` (return real FDT)
  - [ ] Update `shared/constants/addresses_riscv64.go` for Sv48
  - [ ] Delete `uefi_calls_riscv64.s`, `uefi_mem_riscv64.go`
  - [ ] Test: kmazarin prints "[Main] Kmazarin kernel starting..."

---

## Important Constraints

1. **No Go heap allocation** in diplomat. Use `dNew[T]()` (dmalloc.go) or global
   variables. The Go runtime heap depends on `mmap` which routes through the
   syscall overlay — any allocation attempt may panic.

2. **`//go:nosplit` on all low-level functions**. Stack checks use TLS (g register)
   which may not be fully set up. Mark all UART, page table, and fault handler
   functions as nosplit.

3. **Linear map assumption**: After MMU enable, ALL physical memory is accessible
   via `VA = PA + 0xFFFFFFFF00000000`. This is how diplomat accesses page tables,
   UART, MMIO devices, and the kernel binary.

4. **Don't modify shared code semantics**. The `elf_loader.go`, `startup_env.go`,
   and `main.go` are shared across architectures. Use build tags to provide
   arch-specific implementations. Never break ARM64 or x86_64 builds.

5. **Test after every phase**. Run `$GO tool task run-diplomat-riscv64 TIMEOUT=10`
   and check the serial log. If nothing prints, use QEMU monitor to check where
   it stopped.
