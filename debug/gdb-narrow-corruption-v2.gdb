# GDB script to narrow down where x28 corruption happens - Version 2
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-narrow-corruption-v2.gdb build/kmazarin.elf

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Narrowing Down x28 Corruption - Version 2 ===\n\n

# Expected g register value
set $expected_x28 = 0xFFFFFFFF4197BF20

echo Setting breakpoints...\n

# Break after g is initialized (after ADRP + ADD)
break *0xFFFFFFFF4186EB4C
commands
    silent
    stepi
    printf "1. After g init:        PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED!]\n"
    end
    continue
end

# Break at first syscall (sched_getaffinity)
break *0xFFFFFFFF418725B0
commands
    silent
    printf "2. At sched_getaffinity: PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED!]\n"
    end
    continue
end

# Break at getCPUCount entry
break *0xFFFFFFFF41835CC0
commands
    silent
    printf "3. getCPUCount entry:   PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED!]\n"
    end
    continue
end

# Break at getCPUCount exit (return instruction)
break *0xFFFFFFFF41835DD8
commands
    silent
    printf "4. getCPUCount exit:    PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED!]\n"
    end
    continue
end

# Break after getCPUCount returns to osinit
break *0xFFFFFFFF4183646C
commands
    silent
    printf "5. After getCPUCount:   PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED!]\n"
    end
    continue
end

# Break at getHugePageSize entry
break *0xFFFFFFFF41836340
commands
    silent
    printf "6. getHugePageSize:     PC=0x%lx  x28=0x%lx", $pc, $x28
    if ($x28 == $expected_x28)
        printf "  [OK - UNEXPECTED!]\n"
        printf "\nIf x28 is OK here, the crash is for a different reason.\n"
    else
        printf "  [CORRUPTED!]\n"
        printf "\nCorruption happened between checkpoints.\n"
        printf "Disassembly around current location:\n"
        x/10i $pc-40
    end
end

echo \nStarting execution...\n
echo Format: checkpoint  PC  x28  [status]\n\n

continue

echo \n=== Stopped at getHugePageSize ===\n
echo Current register state:\n
info registers x28 pc sp

echo \nType 'continue' to proceed or examine the state.\n
