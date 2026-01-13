# GDB script to find exactly where g (x28) gets corrupted
#
# Usage:
#   Terminal 1: ~/mazzy/bin/qemu-system-aarch64 -S -s -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -device bochs-display -display none -serial stdio -no-reboot
#   Terminal 2: gdb-multiarch -x debug/gdb-find-g-corruption.gdb build/kmazarin.elf

# Connect to QEMU
target remote localhost:1234

# Disable pagination
set pagination off
set print pretty on
set architecture aarch64

echo \n=== Finding g Register Corruption ===\n\n

# Expected g register value
set $expected_x28 = 0xFFFFFFFF4197BF20

echo Setting breakpoints at key points...\n

# Break after g initialization
break *0xFFFFFFFF4186EB4C
commands
    silent
    stepi
    printf "1. After g init:           x28=0x%lx  [OK]\n", $x28
    continue
end

# Break at getCPUCount entry
break *0xFFFFFFFF41835CC0
commands
    silent
    printf "2. getCPUCount entry:      x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED BEFORE ENTRY!]\n"
    end
    continue
end

# Break at FIRST return in getCPUCount (0x41835d48)
break *0xFFFFFFFF41835D48
commands
    silent
    printf "3. getCPUCount return #1:  x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED AT RETURN #1!]\n"
        printf "\nDisassembly before return:\n"
        x/10i $pc-40
    end
    continue
end

# Break at SECOND return in getCPUCount (0x41835d7c)
break *0xFFFFFFFF41835D7C
commands
    silent
    printf "4. getCPUCount return #2:  x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED AT RETURN #2!]\n"
        printf "\nDisassembly before return:\n"
        x/10i $pc-40
    end
    continue
end

# Break at sched_getaffinity (inside getCPUCount)
break *0xFFFFFFFF418725B0
commands
    silent
    printf "5. In sched_getaffinity:   x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK]\n"
    else
        printf "  [CORRUPTED IN SYSCALL!]\n"
    end
    continue
end

# Break right after getCPUCount returns to osinit
break *0xFFFFFFFF4183646C
commands
    silent
    printf "6. After getCPUCount:      x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK - UNEXPECTED!]\n"
    else
        printf "  [CORRUPTED!]\n"
        printf "\nCorruption happened during getCPUCount or its return.\n"
    end
end

# Break at getHugePageSize to confirm corruption
break *0xFFFFFFFF41836340
commands
    silent
    printf "7. At getHugePageSize:     x28=0x%lx", $x28
    if ($x28 == $expected_x28)
        printf "  [OK - VERY UNEXPECTED!]\n"
    else
        printf "  [CORRUPTED - WILL CRASH!]\n"
    end
end

echo \nStarting execution...\n
echo Checkpoints will show x28 value and status.\n\n

continue

echo \n=== Stopped at getHugePageSize ===\n
echo \nCurrent state:\n
info registers x28 pc sp

echo \nType 'continue' to let it crash, or examine state.\n
