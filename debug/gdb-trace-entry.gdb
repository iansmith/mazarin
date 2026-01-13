# GDB script to trace kmazarin entry from Cardinal's jump to the ADRP instruction
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-trace-entry.gdb build/cardinal.elf
#
# This script will:
# 1. Connect to QEMU
# 2. Trace execution from Cardinal's jump to kmazarin entry
# 3. Step through _rt0_arm64_linux -> main -> rt0_go
# 4. Stop at the ADRP that sets x28

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Tracing kmazarin entry ===\n\n

# Break at kmazarin entry point (_rt0_arm64_linux)
echo Setting breakpoint at kmazarin entry (_rt0_arm64_linux at 0xFFFFFFFF41871CB0)...\n
break *0xFFFFFFFF41871CB0

echo \nContinuing to kmazarin entry...\n
continue

echo \n=== At kmazarin entry point ===\n
info register pc sp x0 x1 x28
echo \nDisassembly:\n
x/10i $pc

echo \n=== Stepping through entry ===\n
stepi
echo After ldr x0, [sp] (load argc):\n
info register pc x0
stepi
echo After add x1, sp, #8 (get argv):\n
info register pc x1
stepi

echo \n=== About to call main() ===\n
x/4i $pc
stepi

echo \n=== Inside main() ===\n
info register pc
x/10i $pc

# Step until we hit the call to KernelMain (which calls rt0_go)
echo \nStepping to rt0_go call...\n
break *0xFFFFFFFF4186EB30
continue

echo \n=== At rt0_go.abi0 ===\n
info register pc sp x0 x1 x28
x/20i $pc

echo \n=== Stepping to ADRP x28 instruction ===\n
# rt0_go prologue is a few instructions, ADRP is at offset +0x18
break *0xFFFFFFFF4186EB48
continue

echo \n=== BEFORE ADRP x28 ===\n
info register pc x28
echo Instruction:\n
x/1i $pc
echo Instruction bytes:\n
x/1xw $pc

echo \nDecode ADRP instruction:\n
set $insn = *(uint32_t*)$pc
printf "Instruction word: 0x%08x\n", $insn
printf "Opcode [31]:      %d (1=ADRP, 0=ADR)\n", ($insn >> 31) & 1
printf "immlo [30-29]:    %d\n", ($insn >> 29) & 3
printf "immhi [23-5]:     0x%05x (%d)\n", ($insn >> 5) & 0x7FFFF, ($insn >> 5) & 0x7FFFF
printf "Rd [4-0]:         %d (28=x28)\n", $insn & 0x1F

set $immlo = ($insn >> 29) & 3
set $immhi = ($insn >> 5) & 0x7FFFF
set $imm21 = ($immhi << 2) | $immlo
printf "Combined imm21:   0x%x (%d)\n", $imm21, $imm21

set $page_offset = $imm21 << 12
printf "Page offset:      0x%x\n", $page_offset

set $pc_page = $pc & ~0xFFF
printf "PC page:          0x%016lx\n", $pc_page
printf "Target page:      0x%016lx\n", $pc_page + $page_offset

echo \n=== Executing ADRP ===\n
stepi

echo \n=== AFTER ADRP x28 ===\n
info register x28
printf "x28:              0x%016lx\n", $x28
printf "Expected:         0x%016lx\n", $pc_page + $page_offset

echo \nSession ready. Type 'continue' to proceed or examine registers.\n
