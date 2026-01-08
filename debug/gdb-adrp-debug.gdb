# GDB script to debug ADRP instruction that sets g register (x28)
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-adrp-debug.gdb build/kmazarin.elf
#
# This script will:
# 1. Connect to QEMU
# 2. Set breakpoint at the ADRP instruction that sets x28 in rt0_go
# 3. Display registers before and after ADRP execution
# 4. Single-step through the instruction
# 5. Verify the calculated address

# Connect to QEMU's GDB server
target remote localhost:1234

# Disable pagination
set pagination off

# Display useful information
set print pretty on

# Set architecture
set architecture aarch64

echo \n=== ADRP Debugging Session ===\n\n

# The ADRP instruction is at 0x4186eb48 (low memory) or 0xFFFFFFFF4186eb48 (high memory)
# We'll set a breakpoint at the high-memory address where kmazarin actually executes

echo Setting breakpoint at ADRP instruction (rt0_go.abi0+0x18)...\n
break *0xFFFFFFFF4186eb48

echo \nContinuing to breakpoint...\n
continue

echo \n=== Stopped at ADRP instruction ===\n
echo Current PC: \n
info register pc

echo \n=== Register state BEFORE ADRP ===\n
info register x28
info register sp

echo \n=== Disassembly around ADRP ===\n
x/10i $pc

echo \n=== Instruction bytes at PC ===\n
x/4xw $pc

echo \n=== Single-stepping ADRP instruction ===\n
stepi

echo \n=== Register state AFTER ADRP ===\n
info register x28
info register pc

echo \n=== Expected x28 value: 0xFFFFFFFF4197B000 ===\n
printf "Actual x28:   0x%016lx\n", $x28
printf "Expected:     0xFFFFFFFF4197B000\n"

# Check if x28 is correct
if ($x28 == 0xFFFFFFFF4197B000)
    echo \n*** SUCCESS: x28 has correct value! ***\n
else
    echo \n*** ERROR: x28 has WRONG value! ***\n
    printf "Difference:   0x%016lx\n", $x28 - 0xFFFFFFFF4197B000
end

echo \n=== Memory at g0 location (x28 should point here) ===\n
# g0 is at offset 0xF20 from the page, so x28 + 0xF20
set $g0_addr = $x28 + 0xF20
printf "g0 address (x28 + 0xF20): 0x%016lx\n", $g0_addr
echo Memory at g0:\n
x/8gx $g0_addr

echo \n=== Next instruction (ADD x28, x28, #0xF20) ===\n
x/1i $pc
stepi

echo \n=== After ADD: x28 should equal g0 address ===\n
info register x28
printf "x28:          0x%016lx\n", $x28
printf "Expected g0:  0xFFFFFFFF4197BF20\n"

if ($x28 == 0xFFFFFFFF4197BF20)
    echo \n*** SUCCESS: x28 now points to g0! ***\n
else
    echo \n*** ERROR: x28 does NOT point to g0! ***\n
end

echo \n=== Continuing execution to see if crash still occurs ===\n
# Set breakpoint at getHugePageSize where the crash happens
break *0xFFFFFFFF41836340
continue

echo \n=== Stopped at getHugePageSize (where crash occurs) ===\n
info register x28
info register sp
x/4i $pc

echo \nSession complete. Type 'quit' to exit or 'continue' to keep running.\n
