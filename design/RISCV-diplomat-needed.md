# RISC-V Diplomat: Reimplementing UEFI Services

## Context

On ARM64 and x86_64, diplomat is a UEFI application. It calls UEFI Boot Services
to allocate memory, read the boot disk, and discover hardware. It also relies on
implicit machine state that UEFI firmware establishes before diplomat runs (page
tables, privilege level, stack, console).

UEFI firmware for RISC-V on QEMU is broken. The RISC-V diplomat must provide the
same services and machine state itself, running on top of OpenSBI (M-mode firmware)
instead of UEFI. This document is organized around the UEFI services and state that
diplomat depends on, with our replacement implementation for each.

**What OpenSBI gives us at entry (S-mode):**
- `A0` = hart ID
- `A1` = FDT (Flattened Device Tree) physical address
- `SATP = 0` (MMU off — no page tables, no virtual addresses)
- `SIE = 0` (interrupts disabled)
- RAM starts at `0x80000000`

**Address space**: We use **Sv48** (4-level, 48-bit virtual addresses) to match
ARM64 and x86_64. This shares the L0/L1/L2/L3 shift constants and 4-level walk
code in `paging.go`.

**QEMU command**: The full QEMU invocation with all the same devices as ARM64/x86_64:

```bash
qemu-system-riscv64 \
    -M virt \
    -m 2G \
    -rtc base=utc,clock=host \
    -kernel build/diplomat-riscv64.elf \
    -drive file=build/disk-riscv64.img,format=raw,if=none,id=drive-virtio-disk0 \
    -device virtio-blk-pci,drive=drive-virtio-disk0 \
    -device virtio-gpu-pci \
    -device virtio-keyboard-pci \
    -device virtio-mouse-pci \
    -serial file:/tmp/diplomat-riscv64-serial.log \
    -monitor tcp:127.0.0.1:4447,server,nowait \
    -display cocoa \
    -no-reboot
```

Key differences from the other platforms:
- **No UEFI firmware** — no `-drive if=pflash` flags. QEMU uses its built-in OpenSBI
  automatically when `-kernel` is specified.
- **`-kernel` instead of ESP boot** — QEMU+OpenSBI loads the ELF directly into RAM
  and enters it in S-mode. No FAT32 ESP, no UEFI boot chain.
- **`-device virtio-gpu-pci`** not `virtio-vga` — `virtio-vga` is x86_64/Q35 specific.
  ARM64 also uses `virtio-gpu-pci`.
- **Port 4447** for QEMU monitor (4445=x86_64, 4446=ARM64).
- **No `-debugcon`** — that's an x86_64 I/O port feature. Debug output goes through serial.

Device parity with other platforms:

| Device | x86_64 | ARM64 | RISC-V |
|--------|--------|-------|--------|
| Machine | q35 | virt | virt |
| VirtIO Block | `virtio-blk-pci` | `virtio-blk-pci` | `virtio-blk-pci` |
| VirtIO GPU | `virtio-vga` | `virtio-gpu-pci` | `virtio-gpu-pci` |
| VirtIO Keyboard | `virtio-keyboard-pci` | `virtio-keyboard-pci` | `virtio-keyboard-pci` |
| VirtIO Mouse | `virtio-mouse-pci` | `virtio-mouse-pci` | `virtio-mouse-pci` |
| Serial | file-based | file-based | file-based |
| Monitor port | 4445 | 4446 | 4447 |

**S-mode constraint**: All diplomat code runs in S-mode (supervisor). We never
execute in M-mode (machine) — that is OpenSBI's domain. Everything we do (page
table construction, PLIC initialization, UART access, memory allocation, cache
fencing) uses S-mode instructions and S-mode accessible registers/memory.

The only M-mode involvement is through SBI ecalls, where our S-mode code issues
an `ecall` instruction that traps to OpenSBI in M-mode to perform an operation
we cannot do ourselves (writing CLINT timer compare registers, starting secondary
harts, powering off). This is analogous to a syscall — our code stays in S-mode,
the firmware briefly handles the request in M-mode and returns. Three SBI calls
are used by kmazarin (not diplomat):
- `set_timer` — arm the next timer interrupt (CLINT mtimecmp is M-mode only)
- `hart_start` — boot secondary CPUs
- `shutdown` — power off

These are the ONLY points where M-mode is involved. Diplomat itself makes zero
SBI calls.

---

# Part A: UEFI Boot Services — Replacements

These are the actual UEFI function calls diplomat makes. For each one, we describe
what diplomat uses it for and how the RISC-V diplomat replaces it.

---

## A1. `ConOut->OutputString` — Console Output

### What diplomat does (ARM64/x86_64)
Diplomat calls `systemTable->ConOut->OutputString()` for all boot messages
("Diplomat UEFI Bootloader", "ELF: entry=...", etc.). This is diplomat's only
output mechanism.

**Files**: `uefi_calls_amd64.s:20`, `uefi_calls_arm64.s:17`, `main.go:63-75`

### RISC-V replacement: Direct NS16550A UART writes

QEMU virt provides an NS16550A UART at physical address `0x10000000`. OpenSBI
initializes it during M-mode boot. We write directly to its registers.

**Before MMU enable** (assembly):
```asm
uart_putc:
    li   t0, 0x10000000       # NS16550 base (physical address)
1:  lbu  t2, 5(t0)            # Read LSR (Line Status Register)
    andi t2, t2, 0x20         # Check THRE bit (ready to send?)
    beqz t2, 1b               # Spin until ready
    sb   a0, 0(t0)            # Write character to THR
    ret
```

**After MMU enable** (Go code):
```go
const UARTBase = 0xFFFFFFFF10000000  // PA 0x10000000 via linear map
func printChar(c byte) {
    for *(*byte)(unsafe.Pointer(uintptr(UARTBase + 5))) & 0x20 == 0 {}
    *(*byte)(unsafe.Pointer(uintptr(UARTBase))) = c
}
```

**This is the first thing to implement.** Every subsequent step uses it for
debug output.

Existing code already uses these addresses: `kmazarin/console/breadcrumb_riscv64.go`,
`runtime-patches/sys_linux_riscv64.s`.

---

## A2. `BS->AllocatePages` — Physical Memory Allocation

### What diplomat does (ARM64/x86_64)
Diplomat calls `UEFIAllocatePages()` to obtain physical pages for:
- Page table pool (64 pages / 256KB)
- g0 stack (8 pages / 32KB) and exception stack (4 pages / 16KB)
- Heap demand-paging pool (4096+ pages / 16+MB)
- Unified pool (for kmazarin's allocator)
- DTB page (1 page / 4KB, ARM64 only)

Three allocation types: `AllocateAnyPages`, `AllocateMaxAddress`, `AllocateAddress`.

**Files**: `uefi_mem_amd64.go:38`, `uefi_mem_arm64.go:35`, `uefi_calls_amd64.s:80`,
`uefi_calls_arm64.s:44`

### RISC-V replacement: Bump allocator over FDT-discovered RAM

Implement a simple bump allocator that advances a pointer through usable RAM.

1. **Discover RAM** by parsing the FDT `/memory` node (see A5 below).
2. **Exclude reserved regions**: OpenSBI's own memory (`/reserved-memory` in FDT),
   and the mini-diplomat binary itself.
3. **Bump allocator**: Start from first usable page after the diplomat binary.
   Each `allocatePages(n)` call returns current pointer and advances by `n * 4KB`.
   No free — diplomat never frees pages.

```go
var nextFreePage uintptr  // Initialized from FDT + binary end

//go:nosplit
func allocatePages(count int) uintptr {
    pa := nextFreePage
    nextFreePage += uintptr(count) * 4096
    return pa
}
```

The same pool sizes as ARM64/x86_64 diplomat apply. The allocator replaces all
`UEFIAllocatePages` calls with direct `allocatePages()` calls.

---

## A3. `BS->HandleProtocol(LOADED_IMAGE)` + `BS->HandleProtocol(BLOCK_IO)` + `BlockIO->ReadBlocks` — Disk Access

### What diplomat does (ARM64/x86_64)
Diplomat chains three UEFI calls to read kmazarin from disk:
1. `HandleProtocol(imageHandle, LOADED_IMAGE_PROTOCOL)` → get boot device handle
2. `HandleProtocol(deviceHandle, BLOCK_IO_PROTOCOL)` → get block I/O interface
3. `BlockIO->ReadBlocks(mediaId, LBA, size, buffer)` → read FAT32 sectors

This loads the kmazarin ELF from the ESP (EFI System Partition).

**Files**: `uefi_protocol.go:74-98`, `uefi_blockdev.go:101-108`,
`uefi_calls_arm64.s:80-114`

### RISC-V replacement: Not needed (`-kernel` flag)

QEMU's `-kernel` flag loads the ELF directly into RAM before OpenSBI runs.
The mini-diplomat does not need to read from disk at all.

If disk loading is needed later (self-hosted boot without `-kernel`), kmazarin
already has VirtIO block drivers:
- PCI: `kmazarin/device/virtio/block/`
- MMIO: `kmazarin/virtio/block.go`

---

## A4. `BS->LocateProtocol(MP_SERVICES)` — CPU Count Discovery

### What diplomat does (ARM64/x86_64)
Diplomat calls `LocateProtocol(EFI_MP_SERVICES_PROTOCOL)` then
`GetNumberOfProcessors()` to discover how many CPUs are available.

**Files**: `hardware_amd64.go:75`, `hardware_arm64.go:70`

### RISC-V replacement: Parse FDT `/cpus` node

The FDT contains a `/cpus` node with one child per hart:
```
cpus {
    cpu@0 { device_type = "cpu"; riscv,isa = "rv64imafdcsu"; };
    cpu@1 { ... };
};
```

Count the `cpu@N` children. This also provides the ISA string needed for
extension detection (Svpbmt, Zicbom — see B5, B6 below).

---

## A5. `BS->GetMemoryMap` — Physical Memory Discovery

### What diplomat does (ARM64/x86_64)
Diplomat calls `GetMemoryMap()` to obtain the full physical memory map with
region types (conventional, reserved, MMIO, firmware). This is also required
as a prerequisite for `ExitBootServices`.

**Files**: `uefi_mem_amd64.go:128`, `uefi_mem_arm64.go:117`

### RISC-V replacement: Parse FDT `/memory` and `/reserved-memory`

Implement FDT parsing from the start (no hardcoded addresses). The FDT is
always available — OpenSBI guarantees it in A1. QEMU provides a correct FDT
that exercises the same code paths as real hardware.

Parse:
1. `/memory@80000000` → `reg` property gives RAM base + size
2. `/reserved-memory` → regions to exclude (OpenSBI, etc.)
3. `/chosen` → optional bootargs, stdout-path

A minimal FDT parser needs to handle:
- FDT header (magic `0xD00DFEED`, totalsize, struct/strings offsets)
- `FDT_BEGIN_NODE` / `FDT_END_NODE` / `FDT_PROP` tokens
- Property name lookup via strings block
- `reg` property parsing (pairs of address/size cells)

For reference, QEMU virt with `-m 2G`:
- RAM: `0x80000000` to `0xFFFFFFFF` (2GB)
- OpenSBI: `0x80000000` to `0x80200000` (reserved, ~2MB)

---

## A6. `BS->ExitBootServices` — Reclaim Firmware Memory

### What diplomat does (ARM64/x86_64)
Diplomat calls `ExitBootServices(imageHandle, mapKey)` to terminate UEFI Boot
Services and reclaim firmware memory. After this call, no UEFI services are
available.

**Files**: `uefi_mem_amd64.go:135`, `uefi_mem_arm64.go:123`

### RISC-V replacement: Not needed

There are no boot services to exit. OpenSBI remains resident in M-mode but
does not consume S-mode resources. The FDT `/reserved-memory` describes
OpenSBI's footprint so we don't accidentally overwrite it.

---

# Part B: UEFI-Provided Machine State — What We Must Establish

UEFI sets up significant machine state before diplomat's entry point runs.
Diplomat relies on this state implicitly. On RISC-V, we must establish all
of it ourselves.

---

## B1. Page Tables Active (Identity Map)

### What UEFI provides
UEFI enables the MMU with identity-mapped page tables covering all physical
memory. On x86_64, diplomat reads CR3 and grafts kernel mappings onto the
existing PML4. On ARM64, diplomat builds a separate TTBR1 table for high-VA
kernel mappings while UEFI's TTBR0 handles identity-mapped code.

**Files**: `kernelvm_amd64.go:100-220`, `kernelvm_arm64.go:103-220`,
`pagetable_amd64.go:65-94`

### What we must do
OpenSBI sets `SATP=0` (bare mode — no translation). We must build **Sv48 page
tables from scratch in assembly** before any Go code executes, because Go's
runtime initialization touches high-memory addresses (stack guards, TLS, globals).

#### Page table structure (Sv48, 4 levels — same as ARM64/x86_64)

```
L3 (root, 512 entries, each covers 512GB)
  L2 (512 entries, each covers 1GB) — can be 1GB gigapage leaf
    L1 (512 entries, each covers 2MB) — can be 2MB megapage leaf
      L0 (512 entries, each covers 4KB) — always leaf
```

Same level numbering as `paging.go`: L3Shift=39, L2Shift=30, L1Shift=21, L0Shift=12.

#### Required mappings

| Region | Virtual Address | Physical Address | Size | PTE Level | Flags |
|--------|----------------|-----------------|------|-----------|-------|
| Identity map (boot code) | `0x80000000` | `0x80000000` | 2MB+ | L1 leaf | V+R+W+X+A+D+G |
| Kernel text+data | `0xFFFFFFFF43800000` | PA of loaded ELF | 4MB | L1 leaf | V+R+W+X+A+D+G |
| Linear map (all RAM) | `0xFFFFFFFF00000000+PA` | `0x00000000`–`0xFFFFFFFF` | 4GB | L2 leaf | V+R+W+A+D+G |
| MMIO (UART, PLIC) | `0xFFFFFFFF00000000+PA` | `0x00000000`–`0x3FFFFFFF` | 1GB | L2 leaf | V+R+W+A+D |
| g0 stack | Computed VA | Allocated PA | 32KB | L0 leaf | V+R+W+A+D |
| Exception stack | Computed VA | Allocated PA | 16KB | L0 leaf | V+R+W+A+D |

#### Assembly sequence

```asm
_start:
    # OpenSBI provides: a0=hartid, a1=FDT address
    # Save a0, a1 in s-registers

    # 1. Allocate + zero L3 root table page
    # 2. Fill L3 entry → L2 table for identity-map region
    # 3. Fill L2 entries as 1GB gigapage leaves for linear map
    # 4. Allocate + fill L1/L0 tables for kernel + stacks
    # 5. csrw satp, (MODE=9 << 60) | (PPN of L3 root)   # Sv48
    # 6. sfence.vma
    # 7. Jump to high-memory Go entry point
```

The identity map ensures the instruction after `csrw satp` still fetches correctly
(PC is a physical address that's also identity-mapped). The identity map is removed
after jumping to the high-memory alias.

On x86_64, diplomat does the equivalent by disabling CR0.WP, modifying UEFI's PML4,
and re-enabling CR0.WP. We do this from scratch instead of grafting.

---

## B2. Valid Stack Pointer

### What UEFI provides
UEFI provides a valid SP at entry. Diplomat uses it as g0's initial stack, then
allocates separate stacks for kmazarin via `AllocatePages`.

**Files**: `entry_amd64.s:23-63`, `entry_arm64.s:28-54`

### What we must do
OpenSBI's SP points into firmware memory with unknown size. Set up our own stack
in assembly before calling any Go code:

1. g0 stack: 32KB at a fixed offset after the diplomat binary
2. Exception stack: 16KB immediately after
3. Set SP to top of g0 stack
4. Store exception stack top in SSCRATCH (for trap handler SP swap)
5. Initialize `g0.stack.lo` and `g0.stack.hi`

These must be identity-mapped before MMU enable, then accessible through the
linear map (`VA = PA + KernelMMIOOffset`) after.

---

## B3. Privilege Level (Ring 0 / EL1)

### What UEFI provides
UEFI runs diplomat at the highest usable OS privilege: Ring 0 (x86_64), EL1 (ARM64).

### What we get
OpenSBI drops to S-mode (supervisor) before calling our entry point. S-mode is
the RISC-V equivalent of Ring 0 / EL1 — it can manage page tables, trap vectors,
and interrupt enable/disable. This is correct and requires no action.

---

## B4. GDT / Segmentation (x86_64 only)

### What UEFI provides
UEFI sets up a GDT with code/data segments. Diplomat reads the CS selector for
IDT entries.

### RISC-V
**Not applicable.** RISC-V has no segmentation. Memory protection is entirely
through Sv48 page tables. Skip.

---

## B5. Memory Attribute Configuration (MAIR / MTRRs)

### What UEFI provides
- **ARM64**: UEFI configures MAIR_EL1 with attribute indices for Normal and Device
  memory. Diplomat overrides MAIR to match kmazarin's expected layout.
- **x86_64**: MTRRs configured for WB/UC regions.

**Files**: `kernelvm_arm64.go` (MAIR override)

### What we must do
RISC-V has no MAIR/MTRR equivalent. Memory attributes are controlled per-PTE via
the **Svpbmt** extension (PTE bits 62:61):
- `00` = PMA (use platform defaults — cacheable for RAM)
- `01` = NC (Non-Cacheable)
- `10` = IO (strongly ordered — for MMIO)

Always set IO attributes on MMIO mappings. Gate behind runtime detection so
reserved bits stay zero on hardware without Svpbmt:

```go
const (
    RV_PBMT_PMA = 0 << 61  // Platform default (cacheable RAM)
    RV_PBMT_IO  = 2 << 61  // Strongly ordered (MMIO)
)

var svpbmtSupported bool  // Detected from FDT ISA string

func makeKernelDevicePTE(pa uintptr) uint64 {
    pte := (ppn << 10) | RV_PTE_V | RV_PTE_R | RV_PTE_W | RV_PTE_A | RV_PTE_D
    if svpbmtSupported { pte |= RV_PBMT_IO }
    return pte
}
```

QEMU ignores these bits. Real hardware requires them.

---

## B6. Cache Coherency

### What UEFI provides
- **ARM64**: D-cache and I-cache enabled via SCTLR_EL1.
- **x86_64**: Caches enabled, MTRRs configured.

### What we must do
Caches are enabled by OpenSBI. Always perform fence operations unconditionally —
QEMU executes them cheaply, real hardware requires them.

After writing code to memory (loading kmazarin ELF):
```asm
fence rw, rw    # Data memory fence
fence.i         # I-cache sync (required by spec after writing code)
```

After modifying page tables:
```asm
sfence.vma      # TLB flush
```

For DMA (VirtIO descriptor rings), detect **Zicbom** extension from FDT ISA string:
- If present: `CBO.FLUSH` for cache line clean+invalidate
- If absent: `FENCE RW, RW` (assumes platform DMA coherency)

Barrier mappings in `asm_barriers_riscv64.s`:
- `dsbSYAsm` → `FENCE RW, RW` (always)
- `isbSYAsm` → `FENCE.I` (always)
- `dcCIVACAsm` → `CBO.FLUSH` if Zicbom, else `FENCE RW, RW`
- `tlbiVAE1ISAsm` → `SFENCE.VMA va, zero` (always)
- `tlbiVMALLE1IS` → `SFENCE.VMA zero, zero` (always)

---

## B7. Interrupt Controller Initialized

### What UEFI provides
- **ARM64**: GICv2 active with timer IRQs running. Diplomat disables GICD.
- **x86_64**: LAPIC/IOAPIC active. Diplomat issues CLI.

### What we must do
OpenSBI leaves the **PLIC** (Platform-Level Interrupt Controller) in an
unspecified state. Initialize it before kmazarin enables interrupts:

1. **Set threshold to 0** for S-mode context (`2 * hartid + 1`):
   - Register: `PLIC_BASE + 0x200000 + context * 0x1000`
2. **Set priority** for each interrupt source to non-zero:
   - Register: `PLIC_BASE + source * 4`
3. **Enable sources** in S-mode enable bits:
   - Register: `PLIC_BASE + 0x2000 + context * 0x80`
4. **Drain pending interrupts**:
   ```
   loop:
       claim = read(PLIC_BASE + 0x200000 + context*0x1000 + 4)
       if claim == 0: break
       write(claim)  // complete
   ```

Leave supervisor interrupts **disabled** — kmazarin enables them when ready.

---

## B8. TLS Register

### What UEFI provides
- **x86_64**: Diplomat sets MSR_FS_BASE to `tlsBlock+8` so Go reads `g` from `FS:-8`.
- **ARM64**: Diplomat sets TPIDR_EL0 to `tlsBlock+8` for the same purpose.

**Files**: `entry_amd64.s:92-107`, `entry_arm64.s:64-75`

### What we must do
Go on RISC-V uses the **TP register (X4)**. Set it up after g0 is initialized:

```asm
    la   t0, tlsBlock        # TLS block address
    sd   s10, 0(t0)          # Store g0 pointer at tlsBlock[0]
    addi t0, t0, 8           # TP = tlsBlock + 8
    mv   tp, t0              # Go reads g via: ld reg, -8(tp)
```

Must happen after g0 init but before any Go function with ABI0 wrappers.

---

## B9. Timer Hardware

### What UEFI provides
UEFI has timer events running internally. On ARM64, GIC timer IRQ is active.
On x86_64, LAPIC timer is active.

### What we must do
Nothing. The mini-diplomat leaves the timer unarmed. Kmazarin arms it via
SBI `set_timer` ecall when ready.

**Note on SBI**: SBI (Supervisor Binary Interface) is RISC-V's runtime firmware
call interface. Unlike UEFI Boot Services which disappear after `ExitBootServices`,
SBI ecalls remain available permanently — the kernel traps to M-mode firmware
(OpenSBI) via `ecall`. Kmazarin uses SBI for:
- `set_timer` (arm next timer interrupt)
- `hart_start` (boot secondary CPUs)
- `shutdown` (power off)

This is NOT analogous to anything on x86_64/ARM64 — those platforms access timer
hardware directly through MMIO registers. SBI is more like a hypervisor call.

Existing code: `ktimer/platform_riscv64.s:PlatformRearmTimer`

---

## B10. Alignment Checking (ARM64 SCTLR_EL1.A)

### What UEFI provides (ARM64)
SCTLR_EL1 alignment check configuration.

### RISC-V
No software configuration exists. Misaligned access is handled transparently:
hardware executes natively (Zicclsm extension) or OpenSBI emulates in M-mode.
Same behavior on QEMU and real hardware. Skip.

---

## B11. ACPI Tables

### What UEFI provides
UEFI exposes RSDP in the configuration table. On QEMU, ACPI mode is default.

### RISC-V
RISC-V does not use ACPI. All device discovery is through the FDT from OpenSBI.
Pass the real FDT address (from A1) through to kmazarin via auxv `AT_FDT_ADDR`.
Do not build a synthetic DTB — use OpenSBI's real FDT, which includes
reserved-memory, CPU topology, ISA extensions, and accurate device addresses.

---

# Part C: Diplomat Infrastructure to Build

After establishing machine state (Part B) and replacing UEFI services (Part A),
diplomat builds several data structures for kmazarin. This is the same on all
architectures — only the underlying allocation mechanism changes.

---

## C1. Auxv Boot Parameters (replaces EFI_SYSTEM_TABLE)

UEFI provides the EFI_SYSTEM_TABLE with pointers to services and configuration.
Diplomat extracts what it needs and packages it as Linux-style auxv entries for
kmazarin. The RISC-V diplomat builds the same auxv, sourcing values from the
bump allocator and FDT instead of UEFI:

| Auxv Key | Meaning | Source on RISC-V |
|----------|---------|-----------------|
| AT_TTBR1_L0_PHYS | Root page table PA | Allocated by diplomat |
| AT_TTBR0_L0_PHYS | Process page table PA | Same as TTBR1 (single SATP) |
| AT_FRAME_POOL_START/END | Kernel frame pool | Allocated by diplomat |
| AT_USER_FRAME_POOL_START/END | User frame pool | Allocated by diplomat |
| AT_KERNEL_PT_POOL_START/END | Kernel PT pool | Allocated by diplomat |
| AT_USER_PT_POOL_START/END | User PT pool | Allocated by diplomat |
| AT_UNIFIED_POOL_START/END | Unified allocator | Allocated by diplomat |
| AT_G0_STACK_PA | g0 stack physical address | Allocated by diplomat |
| AT_EXC_STACK_PA | Exception stack PA | Allocated by diplomat |
| AT_KMAZARIN_SIZE | Kernel binary size | From ELF headers |
| AT_FDT_ADDR | FDT physical address | From OpenSBI A1 |
| AT_HWCAP | ISA capabilities | From FDT ISA string |
| AT_NCPUS | CPU count | From FDT `/cpus` node |
| AT_RAM_BASE | RAM start PA | From FDT `/memory` |
| AT_RAM_SIZE | RAM size | From FDT `/memory` |

---

## C2. Kernel Page Table Mappings

Same as ARM64/x86_64 diplomat: map kmazarin's code/data at high VA, set up
linear map for physical memory access, map stack and heap regions. The page
table construction code (`kernelvm_*.go`, `pagetable_*.go`) is the same logic —
only the PTE format differs (already handled by `paging_riscv64.go`).

---

## C3. Demand Paging Fault Handler

Diplomat installs a minimal page fault handler that allocates pages from the
pre-allocated pool during kmazarin initialization. On x86_64 this is IDT[14];
on ARM64 it's the VBAR data abort vector.

On RISC-V: Install STVEC pointing to a trap handler that services `scause=13`
(load page fault) and `scause=15` (store page fault). Set SSCRATCH to exception
stack top. Same demand-paging logic, different trap entry mechanism.

---

# Implementation Order

Each phase builds on the previous. Test after each phase.

### Phase 1: UART Output (replaces ConOut->OutputString)
1. Assembly `_start` receives A0/A1 from OpenSBI
2. Write character to NS16550A at `0x10000000`
3. WFI loop

**Test**: `qemu-system-riscv64 -M virt -kernel diplomat -serial stdio` prints "H".

### Phase 2: Page Tables (replaces UEFI identity map)
1. Build Sv48 tables in assembly (L3 root + L2 gigapages + L1/L0 as needed)
2. `csrw satp, (9 << 60) | PPN` then `sfence.vma`
3. Jump to high-memory alias
4. Print via UART at `0xFFFFFFFF10000000`

**Test**: Characters print from both low and high VA.

### Phase 3: Go Runtime Bootstrap
1. Initialize g0 struct, set SP (replaces UEFI-provided stack)
2. Set TP register (replaces FS_BASE/TPIDR_EL0 setup)
3. Call Go `printString("Diplomat RISC-V starting...")`

**Test**: Go code prints to serial.

### Phase 4: FDT Parsing (replaces GetMemoryMap + LocateProtocol)
1. Parse `/memory` → RAM ranges (replaces `GetMemoryMap`)
2. Parse `/reserved-memory` → excluded regions
3. Parse `/cpus` → CPU count + ISA string (replaces `MP_SERVICES_PROTOCOL`)
4. Detect Svpbmt/Zicbom from ISA string

**Test**: Print discovered RAM size and CPU count.

### Phase 5: Memory Allocation (replaces AllocatePages)
1. Bump allocator over FDT-discovered usable RAM
2. Allocate all pools: PT pool, frame pools, unified pool, stacks

**Test**: Print pool addresses.

### Phase 6: Full Kernel VM Setup
1. Map kernel code at high VA, stacks, heap region, MMIO
2. Set up demand-paging fault handler (replaces diplomat's IDT[14] / VBAR handler)
3. Build auxv (replaces SystemTable)
4. Install STVEC, set SSCRATCH
5. Jump to kmazarin entry point

**Test**: "[Main] Kmazarin kernel starting..." appears.

### Phase 7: Runtime + Devices
1. Kmazarin demand paging works (page faults serviced)
2. Clone threads created (sysmon, templateThread)
3. PLIC initialization (replaces GIC/APIC — see B7)
4. VirtIO device scanning (PCI or MMIO)

**Test**: "[Main] Runtime ready" and VirtIO GPU display.

---

# UEFI Service Mapping Summary

| UEFI Service / State | ARM64/x86_64 Diplomat | RISC-V Diplomat Replacement |
|-----------------------|----------------------|---------------------------|
| `ConOut->OutputString` | UEFI console protocol | Direct NS16550A UART writes |
| `BS->AllocatePages` | UEFI Boot Service | Bump allocator over FDT RAM |
| `BS->GetMemoryMap` | UEFI Boot Service | FDT `/memory` + `/reserved-memory` |
| `BS->ExitBootServices` | Required | Not needed (no services to exit) |
| `HandleProtocol(BLOCK_IO)` | UEFI protocol chain | Not needed (`-kernel` loads ELF) |
| `BlockIO->ReadBlocks` | UEFI block protocol | Not needed (`-kernel` loads ELF) |
| `LocateProtocol(MP_SERVICES)` | UEFI protocol | FDT `/cpus` node |
| Page tables active | UEFI firmware (CR3/TTBR) | Build Sv48 in assembly |
| Valid stack pointer | UEFI-provided | Allocate from RAM |
| Privilege level | Ring 0 / EL1 | S-mode (OpenSBI provides) |
| GDT | UEFI firmware | N/A on RISC-V |
| MAIR / MTRRs | UEFI firmware | Svpbmt PTE bits (runtime-detected) |
| Cache enabled | UEFI firmware | FENCE.I / FENCE RW,RW (always) |
| Interrupt controller | GIC/APIC initialized | PLIC init from scratch |
| TLS register | Diplomat sets FS/TPIDR | Diplomat sets TP |
| Timer | Inherited from UEFI | SBI `set_timer` (by kmazarin) |
| ACPI/RSDP | Config table | FDT from OpenSBI (A1) |
| SystemTable | UEFI data structure | Auxv built by diplomat |

---

# Files to Create / Modify

### New Files

| File | Purpose |
|------|---------|
| `diplomat/main/entry_riscv64.s` | Assembly entry: save A0/A1, UART init, stack, page tables, SATP, TLS, jump to Go |
| `diplomat/main/boot_riscv64.go` | Go entry: FDT parsing, bump allocator, pool allocation, auxv, jump to kmazarin |
| `diplomat/main/pagetable_riscv64.s` | Assembly page table builder (runs at physical addresses before Go) |
| `diplomat/main/uart_riscv64.go` | NS16550A UART driver (replaces ConOut->OutputString) |
| `diplomat/main/fdt_parse.go` | FDT parser: /memory, /reserved-memory, /cpus, ISA string |
| `diplomat/main/plic_riscv64.go` | PLIC initialization (replaces GIC/APIC init) |

### Modified Files

| File | Changes |
|------|---------|
| `Taskfile.yml` | Add `diplomat-riscv64` build + QEMU run targets |
| `diplomat/main/platform_riscv64.go` | Platform ops using bump allocator instead of UEFI services |
| `kmazarin/kmem/paging_riscv64.go` | Verify Sv48 walkPageTable matches diplomat's page tables |
| `shared/constants/addresses_riscv64.go` | Verify addresses match diplomat's mappings |

---

# Open Questions

1. **~~Sv39 vs Sv48~~**: **DECIDED — Sv48.** 4-level, 48-bit VA, matching ARM64/x86_64.

2. **FDT parsing scope**: Minimum: `/memory` reg, `/reserved-memory`, ISA string
   (Svpbmt/Zicbom). Nice to have: `/cpus` count, device addresses. Parse the real
   FDT from the start — no hardcoded addresses.

3. **VirtIO transport**: PCI (ECAM at `0x30000000`) vs MMIO (`0x10001000`+)?
   Both available on QEMU virt. PCI shares driver code with x86_64.
   Recommendation: PCI, with MMIO as fallback.

4. **Multi-hart boot**: OpenSBI starts only hart 0. Secondary harts via SBI HSM
   `hart_start`. Mini-diplomat boots hart 0 only. Kmazarin handles the rest.
