# GDB script to narrow down where x28 corruption happens
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-narrow-corruption.gdb build/kmazarin.elf
#
# Strategy: Set breakpoints at key functions to narrow down where corruption happens

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Narrowing Down x28 Corruption ===\n\n

# Expected g register value
set $expected_x28 = 0xFFFFFFFF4197BF20

# Define helper to check x28
define check_x28
    printf "PC: 0x%016lx  x28: 0x%016lx  ", $pc, $x28
    if ($x28 == $expected_x28)
        echo [OK]\n
        set $x28_ok = 1
    else
        echo [CORRUPTED!]\n
        set $x28_ok = 0
    end
end

echo Setting strategic breakpoints...\n\n

# Break after g is initialized
break *0xFFFFFFFF4186EB4C
commands
    silent
    stepi
    printf "1. After g initialization:     "
    check_x28
    continue
end

# Break at first syscall (sched_getaffinity) - we know this succeeds
break *0xFFFFFFFF418725B0
commands
    silent
    printf "2. At sched_getaffinity:       "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 corrupted BEFORE first syscall! ***\n
        echo Corruption happened during runtime initialization.\n
    end
    continue
end

# Break at getCPUCount entry
break *0xFFFFFFFF41835CC0
commands
    silent
    printf "3. At getCPUCount entry:       "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 corrupted BEFORE getCPUCount! ***\n
    end
    continue
end

# Break at getCPUCount exit (return instruction)
break *0xFFFFFFFF41835DD8
commands
    silent
    printf "4. At getCPUCount exit:        "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 corrupted INSIDE getCPUCount! ***\n
    end
    continue
end

# Break right after getCPUCount returns to osinit
break *0xFFFFFFFF4183646C
commands
    silent
    printf "5. After getCPUCount returns:  "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 corrupted when returning from getCPUCount! ***\n
    end
    continue
end

# Break at the ADRP between getCPUCount and getHugePageSize
break *0xFFFFFFFF4183646C
commands
    silent
    printf "6. At ADRP x27 instruction:    "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 already corrupted before ADRP x27! ***\n
    end
    continue
end

# Break at getHugePageSize entry
break *0xFFFFFFFF41836340
commands
    silent
    printf "7. At getHugePageSize entry:   "
    check_x28
    if ($x28_ok == 0)
        echo \n*** x28 corrupted BEFORE getHugePageSize! ***\n
        echo This confirms corruption happened between getCPUCount and here.\n
        echo \nChecking previous instructions...\n
        x/10i $pc-40
    else
        echo \n*** UNEXPECTED: x28 is correct at getHugePageSize! ***\n
        echo The crash might be for a different reason.\n
    end
    # Stop here for investigation
    set $x = 0
end

echo \nStarting execution with checkpoints...\n
echo Format: checkpoint_name  PC  x28_value  [status]\n\n

continue

echo \n=== Stopped at final checkpoint ===\n
echo Type 'continue' to proceed or examine the state.\n
