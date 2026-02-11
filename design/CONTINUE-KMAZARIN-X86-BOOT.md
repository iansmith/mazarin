# Kmazarin x86_64 Boot Crash Investigation - Next Steps

## Status Summary

**What Works ✅:**
- Diplomat UEFI bootloader builds and runs
- ELF loading and symbol extraction
- GDT setup with standard Ring 0/3 descriptors (CS=0x08, SS=0x10)
- TSS setup with exception stack
- Far return to reload CS from UEFI's 0x38 to 0x08 (offset bug FIXED: 9→10 bytes)
- SYSCALL MSR configuration (LSTAR, STAR, FMASK)
- FS_BASE initialization for Go TLS
- IDT installation with diplomat demand paging handler
- **Stack mapping VERIFIED**: All 8 g0 stack pages mapped at VA 0xFFFFFFFF43E00000-0xFFFFFFFF43E07FFF
- **Page table verification**: PML4→PDPT→PD→PT walk confirms VA 0xFFFFFFFF43E07D00 is mapped to PA 0x7D97F000
- **Execution reaches kmazarin entry point**: Breadcrumbs confirm jump to 0x43873740 succeeds

**What Fails ❌:**
- Kmazarin produces NO output after jump (not even one character)
- No page fault messages (stack should be causing faults if unmapped, but it IS mapped)
- Silent crash/hang with no visible exception or output

**Debug Breadcrumbs (diplomat/main/uefi_calls_amd64.s):**
```
1 = Entered jumpToKmazarinWithStack
2 = Before LGDT
3 = After LGDT, before LTR
L = After LTR
F = Before far return
4 = After far return (CS reloaded to 0x08)
5 = Before SYSCALL MSR setup
6 = Before LIDT
7 = After LIDT
J = After switching RSP to g0 stack
K = Right before JMP to kmazarin entry point
```
All breadcrumbs present → we reach kmazarin!

---

## TODO List: Diagnosing the Silent Crash

### Priority 1: Exception Handler Coverage

**Problem**: We only have IDT handlers for vectors 14 (page fault), 48 (timer), 128 (syscall), 255 (spurious). If kmazarin generates ANY other exception (GPF #13, invalid opcode #6, divide by zero #0, etc.), the CPU triple-faults.

**Tasks**:
1. [ ] **Add catch-all exception handlers for all 32 x86_64 exception vectors**
   - File: `diplomat/main/exc_vectors_amd64.s`
   - Add simple handlers that output debug character then halt
   - Vectors to cover: 0-7, 8-13, 15-31 (exclude 14 which we have)
   - Each handler outputs unique character (A-Z) to debug port 0xE9 before halting

2. [ ] **Specifically add GPF (#13) handler with detailed diagnostics**
   - GPF is most likely culprit (invalid segment selector, stack issues, etc.)
   - Output: Error code (pushed by CPU), faulting RIP, segment selectors
   - Check if error code indicates segment-related fault

3. [ ] **Add double fault (#8) handler**
   - Double fault means exception handler itself faulted
   - Has its own stack (IST in TSS) - may need to configure
   - Output breadcrumb to distinguish from triple fault

### Priority 2: QEMU Detailed Logging

**QEMU has extensive logging capabilities that can show exactly what's happening at CPU/MMU level.**

**Tasks**:
4. [ ] **Enable QEMU CPU execution logging**
   - Add to QEMU flags: `-d cpu,int,exec,in_asm -D /tmp/qemu-cpu.log`
   - Shows every instruction executed, register state, interrupts/exceptions
   - WARNING: Generates HUGE logs - use with short timeout (1-2s)
   - Look for last executed instruction before crash

5. [ ] **Enable QEMU MMU/paging logging**
   - Add to QEMU flags: `-d mmu,cpu_reset -D /tmp/qemu-mmu.log`
   - Shows TLB misses, page table walks, CR3 changes
   - Can verify if stack access actually uses mapped PTE

6. [ ] **Use QEMU's SEPARATE_OUTPUT for cleaner logs**
   - Instead of `-D /tmp/qemu-cpu.log`, use separate files per category:
   - `-d cpu -D /tmp/qemu-cpu.log -d int -D /tmp/qemu-int.log`
   - Prevents mixing different log types

7. [ ] **Check QEMU trace events**
   - Add: `-trace 'kvm_*' -trace 'x86_*'` for detailed x86 events
   - Or use `-trace help` to see available trace points

### Priority 3: QEMU Monitor / GDB Integration

**Use QEMU's built-in debugging to inspect state at crash point.**

**Tasks**:
8. [ ] **Query QEMU monitor after timeout to see where execution stopped**
   - Already have monitor on tcp:127.0.0.1:4445
   - After timeout, send: `info registers` to see RIP, RSP, CR0, CR3, etc.
   - Send: `x/10i $rip` to disassemble at current RIP
   - Send: `info mem` to see page table mappings
   - Check if RIP is stuck in a loop or at an unexpected address

9. [ ] **Use GDB remote debugging**
   - Add QEMU flag: `-s -S` (listen on :1234, wait for GDB)
   - Connect: `gdb build/kmazarin-amd64.elf`, then `target remote :1234`
   - Set breakpoint: `b *0x43873740` (kmazarin entry)
   - Single-step through first instructions: `si`, `info registers`
   - Check stack: `x/10gx $rsp`

10. [ ] **Add QEMU breakpoint via monitor**
    - Send to monitor: `gdbserver` to start GDB stub
    - Or: `breakpoint 0x43873740` to break at kmazarin entry

### Priority 4: Kmazarin Entry Point Verification

**Verify kmazarin's first instructions are safe to execute in our environment.**

**Tasks**:
11. [ ] **Disassemble kmazarin entry point and verify assumptions**
    - Check: `objdump -d build/kmazarin-amd64.elf | grep -A20 _rt0_amd64`
    - First instruction: `mov rdi, [rsp]` - reads argc from stack
    - Verify RSP value is correct (should be 0xFFFFFFFF43E07D00)
    - Verify [RSP] contains valid argc value (we set it to 1)

12. [ ] **Check if Go runtime expects specific CPUID features**
    - Go 1.25 may check for SSE, AVX, etc. via CPUID
    - QEMU `-cpu max` should provide all features, but verify
    - Add debug to see if CPUID instruction is executed

13. [ ] **Verify TLS (FS_BASE) is correct**
    - Go runtime needs FS_BASE-8 to contain g pointer
    - We set FS_BASE to `initialTLSBuf+16` (diplomat/main/uefi_calls_amd64.s:660-666)
    - Check if this is accessed correctly

### Priority 5: State Verification Before Jump

**Ensure all CPU state is correct when jumping to kmazarin.**

**Tasks**:
14. [ ] **Verify RSP points to valid startup env structure**
    - RSP should be 0xFFFFFFFF43E07D00
    - [RSP+0] = argc = 1
    - [RSP+8] = argv[0] = pointer to "kmazarin" string
    - Add debug output to print first 64 bytes at RSP before jump

15. [ ] **Verify CR3 is correct**
    - Should be diplomat's PML4 physical address
    - Add debug output before jump: print CR3 value
    - Compare with vm.PML4Phys (should match)

16. [ ] **Verify segment selectors are all 0x08/0x10**
    - After far return and segment reload, all should be Ring 0
    - CS=0x08, DS=ES=SS=0x10, FS=GS=0
    - Add debug to output segment registers before jump

17. [ ] **Check if interrupts disabled (IF=0 in RFLAGS)**
    - We do CLI before jump
    - Kmazarin might expect interrupts disabled initially
    - Verify RFLAGS with QEMU monitor

### Priority 6: Alternative Approaches

**If above doesn't reveal the issue, try different strategies.**

**Tasks**:
18. [ ] **Test with minimal kmazarin**
    - Create tiny test kernel that just outputs 'X' and halts
    - Build as static Go binary at same address (0x43800000)
    - Verify diplomat can jump to it successfully
    - Gradually add Go runtime features to find breaking point

19. [ ] **Compare with working ARM64 boot**
    - ARM64 diplomat+kmazarin works perfectly
    - Compare initialization sequences
    - Check if x86_64 is missing any critical setup

20. [ ] **Try different entry point**
    - Instead of _rt0_amd64_linux, jump directly to runtime.rt0_go
    - Or create custom entry point that sets up state Go expects

21. [ ] **Add kmazarin overlay to output breadcrumb at entry**
    - Modify first instruction of _rt0_amd64 to output debug char
    - This proves we actually reach kmazarin code vs crashing during jump
    - File: create `kmazarin/runtime/entry_debug_amd64.s` overlay

---

## Investigation Prompt for Next Session

```
TASK: Fix kmazarin x86_64 boot crash - kmazarin produces no output after jump

CONTEXT:
- Diplomat successfully boots, loads kmazarin ELF, sets up page tables, GDT, IDT
- Stack is VERIFIED mapped: VA 0xFFFFFFFF43E00000-0xFFFFFFFF43E07FFF → PA verified via PTE walk
- Execution reaches kmazarin entry point at 0x43873740 (confirmed by debug breadcrumbs)
- BUT: Kmazarin produces NO output (not even one character to serial or debug port)
- No page fault exceptions visible
- Silent crash or hang with no visible error

LIKELY CAUSES:
1. Exception not covered by minimal IDT (e.g., GPF #13, invalid opcode #6)
2. Infinite loop in kmazarin early initialization
3. Writing output to wrong location (VGA instead of serial?)
4. TLB/cache coherency issue preventing proper memory access

INVESTIGATION STEPS:

START WITH: Enable QEMU detailed CPU logging to see exactly where execution stops

1. Run with CPU logging:
   ```bash
   # Modify Taskfile.yml run-diplomat-kmazarin task to add:
   -d cpu,int,exec -D /tmp/qemu-cpu.log
   ```

   Then run:
   ```bash
   export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
   export QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
   $GO tool task run-diplomat-kmazarin TIMEOUT=2
   ```

   Check the log:
   ```bash
   tail -200 /tmp/qemu-cpu.log
   ```

   Look for:
   - Last executed RIP address
   - Any exceptions (INT vector numbers)
   - Register state at crash (especially RIP, RSP, RFLAGS)

2. If no useful info from CPU log, add catch-all exception handlers:

   Edit `diplomat/main/exc_vectors_amd64.s`, add after existing handlers:
   ```asm
   // Catch-all exception handlers
   TEXT diplomatExceptionGPF(SB), NOSPLIT, $0
       // Output 'G' for GPF
       MOVB    $'G', AL
       MOVW    $0xE9, DX
       OUTB
       // Infinite halt loop
   gpf_halt:
       HLT
       JMP     gpf_halt

   TEXT diplomatExceptionInvalid(SB), NOSPLIT, $0
       MOVB    $'I', AL
       MOVW    $0xE9, DX
       OUTB
   invalid_halt:
       HLT
       JMP     invalid_halt

   // Add more for vectors 0-31 as needed
   ```

   Update `InstallFaultHandler` to install these:
   ```go
   // In diplomat/main/kernelvm_amd64.go InstallFaultHandler():

   // Add handlers for common exceptions
   setDiplomatIDTEntry(0, getDiplomatDivideByZeroAddr(), cs)  // #DE
   setDiplomatIDTEntry(6, getDiplomatInvalidOpcodeAddr(), cs)  // #UD
   setDiplomatIDTEntry(13, getDiplomatGPFAddr(), cs)          // #GP
   // ... etc for all vectors 0-31
   ```

3. If exception handlers don't trigger, use QEMU monitor to inspect state:

   After QEMU times out:
   ```bash
   echo "info registers" | nc -w 1 127.0.0.1 4445 | grep RIP
   echo "x/10i \$rip" | nc -w 1 127.0.0.1 4445
   echo "info mem" | nc -w 1 127.0.0.1 4445
   ```

   Check:
   - Is RIP stuck in a loop at the same address?
   - Is RIP at an unexpected address (not kmazarin code range)?
   - Are page tables still valid?

4. If still unclear, use GDB for single-stepping:

   Modify QEMU command to add `-s -S` (wait for GDB):
   ```bash
   # Taskfile.yml: add to qemu-system-x86_64 flags
   -s -S
   ```

   In another terminal:
   ```bash
   gdb build/kmazarin-amd64.elf
   (gdb) target remote :1234
   (gdb) b *0x43873740    # Break at kmazarin entry
   (gdb) c                 # Continue to breakpoint
   (gdb) si                # Single-step instructions
   (gdb) info registers
   (gdb) x/10gx $rsp       # Examine stack
   ```

5. Add kmazarin entry breadcrumb as last resort:

   Create overlay that modifies first instruction:
   ```asm
   // kmazarin/runtime/entry_debug_amd64.s
   TEXT _rt0_amd64(SB),NOSPLIT,$0
       // Output 'M' to prove we reached kmazarin
       BYTE $0xB0; BYTE $'M'           // MOV AL, 'M'
       BYTE $0xBA; WORD $0xE9; WORD $0x00  // MOV DX, 0xE9
       BYTE $0xEE                      // OUT DX, AL

       // Original first instruction
       MOVQ    0(SP), DI
       LEAQ    8(SP), SI
       JMP     runtime·rt0_go(SB)
   ```

EXPECTED OUTCOME:
- CPU log shows last executed instruction and any exception
- Exception handlers (if triggered) output diagnostic character
- OR: GDB single-stepping reveals exact instruction that fails
- With this info, we can determine root cause (missing exception handler, bad state, etc.)

FILES TO CHECK:
- /tmp/qemu-cpu.log - CPU execution trace
- /tmp/diplomat-debug.log - Debug port output (breadcrumbs)
- /tmp/diplomat-serial.log - Serial console output
- diplomat/main/exc_vectors_amd64.s - Exception handlers
- diplomat/main/uefi_calls_amd64.s - Jump sequence
- diplomat/main/kernelvm_amd64.go - Stack mapping and verification
```

---

## Quick Reference: Debug Port Breadcrumbs

**Diplomat (uefi_calls_amd64.s jumpToKmazarinWithStack):**
- `1`: Entered function
- `2`: Before LGDT
- `3`: After LGDT
- `L`: After LTR
- `F`: Before far return
- `4`: After far return (CS=0x08)
- `5`: Before SYSCALL setup
- `6`: Before LIDT
- `7`: After LIDT
- `J`: After RSP switch to g0 stack
- `K`: Right before JMP to kmazarin

**Expected New Breadcrumbs:**
- `M`: Kmazarin entry point reached (if overlay added)
- `G`: General Protection Fault (#13)
- `I`: Invalid Opcode (#6)
- `P`+`F`: Page fault occurred (from exc_vectors_amd64.s)

**Reading debug log:**
```bash
cat /tmp/diplomat-debug.log | tr -d '\0' | cat -v
```

If you see `123LF4567JK` but no `M`, kmazarin entry point wasn't reached OR crashed before outputting.
If you see `123LF4567JKM`, kmazarin entry was reached but crashed immediately after.
If you see `123LF4567JKG`, General Protection Fault occurred.

---

## Memory Layout Reference

**Stack (g0):**
- VA: 0xFFFFFFFF43E00000 - 0xFFFFFFFF43E07FFF (32KB, 8 pages)
- PA: 0x7D979000 - 0x7D980FFF (example from last run)
- RSP points to: 0xFFFFFFFF43E07D00 (startup env structure)

**Kmazarin Code:**
- VA: 0x437FF000 - 0x43B56000 (0x357 pages = 3.5MB)
- PA: 0x77E00000 - 0x77E57000
- Entry: 0x43873740 (_rt0_amd64_linux)

**Page Tables:**
- PML4: 0x7FC01000 (physical)
- Uses UEFI's existing PML4, modified with kernel mappings

**Linear Map:**
- Maps all physical RAM at VA = PA + 0xFFFFFFFF00000000
- Uses 2MB pages
- Skips regions with existing 4KB mappings (stack, kernel code)

**Heap (for demand paging):**
- VA: 0xFFFF800100000000 - 0xFFFF900000000000
- Uses page fault handler to allocate on demand
- NOTE: Stack faults are NOT handled by demand paging!

---

## Key Learnings from This Session

1. **Far return offset bug**: The offset calculation from POPQ to cs_reloaded was wrong (9 vs 10 bytes). This was causing jumps to wrong addresses.

2. **Page fault handler limitations**: Diplomat's PF handler only handles heap range (0xFFFF8001...). Faults outside this range (including stack) go to halt. This is intentional since stack should be pre-mapped.

3. **Stack mapping is complex**: Need to verify at all levels (PML4→PDPT→PD→PT) that entries are present and point to correct physical pages.

4. **Debug breadcrumbs are essential**: Without them, impossible to know how far execution got before crashing.

5. **QEMU logging is powerful**: Can show exact instruction that fails, all register state, MMU activity.

6. **Minimal IDT is risky**: With only PF handler installed, any other exception causes triple fault with no diagnostic output.
