# GDB script to find where x28 (g register) gets corrupted
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-find-x28-corruption.gdb build/kmazarin.elf
#
# This script will:
# 1. Break at ADRP where x28 is set correctly
# 2. Set a watchpoint on x28 to catch when it changes
# 3. Continue execution and stop when x28 is modified
# 4. Show the instruction that corrupted x28

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Finding x28 Corruption ===\n\n

# Break at the ADD instruction (after ADRP sets x28 correctly)
echo Setting breakpoint after x28 is initialized...\n
break *0xFFFFFFFF4186EB4C

echo Continuing to breakpoint...\n
continue

echo \n=== Stopped after ADRP (x28 should be correct) ===\n
info register x28 pc

# Execute the ADD to finish setting g register
echo \nExecuting ADD instruction to complete g initialization...\n
stepi

echo \n=== After ADD: x28 initialized ===\n
info register x28
printf "x28 = 0x%016lx (expected: 0xFFFFFFFF4197BF20)\n", $x28

if ($x28 == 0xFFFFFFFF4197BF20)
    echo *** x28 correctly initialized ***\n
else
    echo *** ERROR: x28 not initialized correctly! ***\n
    quit
end

# Set watchpoint on x28
# Note: ARM64 watchpoints in QEMU/GDB work on memory addresses, not registers
# We need to use a different approach - set breakpoints and check x28 frequently

echo \n=== Strategy: Single-step and check x28 after each instruction ===\n
echo This will be slow but will catch the exact instruction that corrupts x28.\n
echo \n

# Set variable to track x28
set $expected_x28 = 0xFFFFFFFF4197BF20
set $step_count = 0
set $max_steps = 10000

# Single-step loop
define check_x28_loop
    set $step_count = $step_count + 1

    # Single step
    stepi

    # Check if x28 changed
    if ($x28 != $expected_x28)
        printf "\n*** x28 CORRUPTED after %d steps! ***\n", $step_count
        printf "Old value: 0x%016lx\n", $expected_x28
        printf "New value: 0x%016lx\n", $x28
        printf "PC:        0x%016lx\n", $pc
        echo \nInstruction that corrupted x28:\n
        x/1i $pc-4
        echo \nDisassembly around corruption:\n
        x/10i $pc-20
        echo \n=== Register state at corruption ===\n
        info registers
        loop_break
    end

    # Progress indicator every 100 steps
    if ($step_count % 100 == 0)
        printf "Stepped %d instructions, x28 still correct (PC=0x%lx)\r", $step_count, $pc
    end

    # Check if we reached max steps or target function
    if ($step_count >= $max_steps)
        printf "\nReached %d steps without finding corruption.\n", $max_steps
        printf "Current PC: 0x%016lx\n", $pc
        echo Try increasing max_steps or check if we passed getHugePageSize.\n
        loop_break
    end

    # Check if we reached getHugePageSize (where we know corruption has occurred)
    if ($pc >= 0xFFFFFFFF41836340 && $pc <= 0xFFFFFFFF41836350)
        printf "\n*** Reached getHugePageSize at PC=0x%016lx ***\n", $pc
        printf "x28 = 0x%016lx\n", $x28
        if ($x28 != $expected_x28)
            echo x28 was already corrupted before reaching this function!\n
            echo The corruption happened in previous steps.\n
        else
            echo x28 is still correct at entry to getHugePageSize!\n
            echo The crash might be for a different reason.\n
        end
        loop_break
    end

    # Continue loop
    check_x28_loop
end

echo Starting single-step loop (this may take a while)...\n
echo Watching for x28 corruption...\n\n

check_x28_loop

echo \n=== Loop ended ===\n
echo Type 'continue' to resume or 'quit' to exit.\n
