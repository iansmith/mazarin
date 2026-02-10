# RISC-V Port Status

## Current State

**Build Status**: ✅ Complete
- kmazarin-riscv64.elf builds successfully (2.8 MB)
- diplomat-riscv64.efi builds successfully (1.08 MB)
- All RISC-V-specific architecture files created
- Overlay generation working
- ELF fixup applied

**Boot Status**: ⚠️ Partially Working
- OpenSBI v1.7 boots successfully
- OpenSBI loads kmazarin at physical address 0x43800000
- OpenSBI transfers control to kmazarin in S-mode
- **Kmazarin produces NO output after OpenSBI**

## Issues Discovered

### 1. UEFI Boot Path (Original Approach)

**Problem**: UEFI shell can't execute .efi files on RISC-V virt platform
- Shell says: `'FS0:\EFI\BOOT\BOOTRISCV64.EFI' is not recognized`
- This is a known limitation of Homebrew QEMU's RISC-V UEFI firmware
- Boot Manager also fails (VirtIO block device not recognized)

**Root Cause**:
- Homebrew edk2-riscv-code.fd is likely outdated
- RISC-V virt platform support incomplete in this build
- Shell execution and Boot Manager both broken

**Fix**: Build fresh EDK2 firmware from upstream tianocore/edk2

### 2. Direct Kernel Boot (Current Approach)

**Setup**:
- OpenSBI firmware (-bios opensbi-riscv64-generic-fw_dynamic.bin)
- Direct kernel load (-kernel kmazarin-riscv64.elf)
- No UEFI, no diplomat

**Problem**: Kmazarin crashes silently before producing any output

**Root Causes Identified**:

#### A. No Page Tables
- OpenSBI loads kmazarin with SATP=0 (bare mode, no virtual memory)
- Kmazarin expects page tables to be set up by a bootloader (diplomat/cardinal)
- Any access to high memory (0xFFFFFFFFxxxxxxxx) will fault immediately
- Exception handlers aren't set up yet, so faults hang the system

**Fixed**:
- ✅ Changed early_init_riscv64.go to use physical UART (0x10000000)
- ✅ Changed breadcrumb_riscv64.go to use physical UART (0x10000000)

**Still Needed**:
- ❌ Page table setup before accessing ANY kernel code/data
- ❌ All MMIO device access needs physical addresses until paging enabled
- ❌ Go runtime data structures need to be in physical memory range

#### B. Architecture-Specific Code Missing/Incomplete

**Missing Functions**:
- EnableTimerIRQ() - Uses GIC (ARM64-specific), needs RISC-V SBI timer
- initCachedIC() - Uses GIC, needs PLIC equivalent
- VirtIO GPU/Input init - May access high-memory MMIO addresses

**Build Tag Issues**:
- simpleMain() in main.go has no build tags
- Calls ARM64-specific functions (SetVBAR exists but semantics differ)
- Need RISC-V-specific main init path OR conditional compilation

### 3. Memory Layout Assumptions

**ARM64 kmazarin**:
- Expects Cardinal to set up:
  - Identity map (TTBR0) for low memory
  - High-memory map (TTBR1) for kernel at 0xFFFFFFFFxxxxxxxx
  - Exception stacks mapped and ready
  - UART/MMIO devices mapped
- Then Cardinal jumps to kmazarin with MMU enabled

**RISC-V Direct Boot**:
- OpenSBI gives us:
  - Bare mode (SATP=0, no paging)
  - Physical memory access only
  - PMP configured to allow S-mode access to devices
  - A0 = 0 (hartid), A1 = FDT address (0xFFE00000)
- Kmazarin must:
  - Set up own page tables (Sv39)
  - Map itself to high memory
  - Map MMIO devices
  - Set SATP and enable paging
  - THEN start normal initialization

## What Needs To Be Done

### Phase 1: Minimal Boot (Get "Hello World" Output)

**Goal**: See "[Main] Kmazarin kernel starting..." on serial console

**Tasks**:
1. Create RISC-V-specific early boot entry point
   - Assembly code that runs BEFORE Go runtime
   - Sets up minimal Sv39 page tables:
     * Identity map: 0x43800000 → 0x43800000 (kernel .text)
     * High memory map: 0xFFFFFFFF43800000 → 0x43800000
     * MMIO map: 0xFFFFFFFF10000000 → 0x10000000 (UART)
   - Sets SATP register to enable paging
   - Flushes TLB (SFENCE.VMA)
   - THEN jumps to Go entry point

2. Create RISC-V-specific simpleMain() or conditional compilation
   - Skip ARM64-specific init (GIC, SetVBAR for EL1, etc.)
   - Use RISC-V equivalents:
     * SetVBAR → set stvec CSR
     * EnableIRQs → set sstatus.SIE
     * EnableTimerIRQ → SBI set_timer + set sie.STIE
   - Skip VirtIO init until paging works

3. Test with minimal init
   - Early console output working
   - Verify paging is functional
   - Verify exception handlers work

### Phase 2: Device Support

**Tasks**:
1. Implement RISC-V timer via SBI
   - Use SBI set_timer (ecall) instead of GIC
   - Handle timer interrupts in trap handler
   - Integrate with ktimer package

2. Implement PLIC (Platform-Level Interrupt Controller)
   - Replaces GIC on RISC-V
   - Needed for VirtIO device interrupts
   - MMIO at 0x0C000000 on QEMU virt

3. VirtIO Device Support
   - Ensure all MMIO access uses high-memory mapped addresses
   - Verify page tables cover VirtIO MMIO regions
   - Test GPU, keyboard, mouse, block devices

### Phase 3: Userspace Support

**Tasks**:
1. Clone/thread creation
   - Verify ThreadContext save/restore works
   - Test context switch (load_context_and_sret)
   - Verify TLS (thread-local storage) handling

2. Launch dapope.elf and stdio.elf
   - Test userspace page fault handling
   - Verify syscall dispatch
   - Test keyboard/mouse input

**Success Criteria** (from user):
> "The kernel will be declared 'working' when it can run dapope.elf
> and stdio.elf as user programs and respond to the keyboard and mouse."

## Technical Details

### RISC-V Sv39 Page Table Format

```
3-level page tables:
- L2 (root): bits[38:30] - 1GB per entry
- L1: bits[29:21] - 2MB per entry (can be leaf)
- L0: bits[20:12] - 4KB per entry (always leaf)

PTE format:
- Bits [53:10]: PPN (Physical Page Number)
- Bit [7]: D (Dirty)
- Bit [6]: A (Accessed)
- Bit [5]: G (Global)
- Bit [4]: U (User)
- Bit [3]: X (Executable)
- Bit [2]: W (Writable)
- Bit [1]: R (Readable)
- Bit [0]: V (Valid)

Leaf detection: If R|W|X bits are set, it's a leaf (not a branch)
```

### RISC-V CSRs

```
stvec    - Trap vector base address (replaces VBAR_EL1)
sepc     - Exception program counter (replaces ELR_EL1)
sstatus  - Status register (SIE bit enables interrupts)
scause   - Trap cause (interrupt bit + exception code)
stval    - Trap value (fault address for page faults)
satp     - Address translation (page table root + mode)
sie      - Interrupt enable (STIE=timer, SEIE=external)
sip      - Interrupt pending
```

### OpenSBI Boot Parameters

```
A0 = hartid (0 for single-core)
A1 = FDT address (0xFFE00000 on QEMU virt)
SATP = 0 (bare mode, no paging)
SIE = 0 (interrupts disabled)
```

## Files Modified

1. kmazarin/kmazarin/early_init_riscv64.go - Use physical UART
2. kmazarin/console/breadcrumb_riscv64.go - Use physical UART
3. Taskfile.yml - Added run-direct-riscv64 task

## Files That Need Work

1. NEW: kmazarin/kmazarin/boot_riscv64.s - Early boot assembly
2. NEW: kmazarin/kmem/pagetable_init_riscv64.go - Page table setup
3. kmazarin/kmazarin/main.go - Conditional init for RISC-V
4. kmazarin/kirq/* - Timer via SBI, PLIC support
5. kmazarin/device/virtio/* - Physical→high memory transition

## References

- OpenSBI Documentation: https://github.com/riscv-software-src/opensbi/blob/master/docs
- RISC-V Privileged Spec: https://github.com/riscv/riscv-isa-manual/releases
- QEMU RISC-V virt: https://www.qemu.org/docs/master/system/riscv/virt.html
- PLIC Specification: https://github.com/riscv/riscv-plic-spec
