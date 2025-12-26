# Find where 0xDEAD000E comes from by examining state at crash

target remote :1234
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000
set architecture aarch64

# Break right after kmazarin starts
break *0x41872510
commands
    silent
    printf "\n=== Kmazarin entry point ===\n"
    printf "Checking globalAlloc.persistent.base at START:\n"
    x/gx 0x419a1068
    continue
end

# Break at first mmap syscall
break SyscallMmap
commands
    silent
    printf "\n=== First mmap call ===\n"
    printf "Before mmap, globalAlloc.persistent.base:\n"
    x/gx 0x419a1068
    finish
    printf "After mmap returns, X0 = 0x%lx\n", $x0
    printf "After mmap, globalAlloc.persistent.base:\n"
    x/gx 0x419a1068
    continue
end

# Break at the crash location
break *0x41813f8c
commands
    silent
    printf "\n"
    printf "============================================================\n"
    printf "AT CRASH POINT!\n"
    printf "============================================================\n"
    printf "\n"
    printf "PC = 0x%lx\n", $pc
    printf "X4 (pointer to persistent.base) = 0x%lx\n", $x4
    printf "\n"
    printf "Value at [X4] (this is persistent.base):\n"
    x/gx $x4
    printf "\n"
    printf "Memory around X4:\n"
    x/16gx $x4-64
    printf "\n"
    printf "This value will be loaded into X3 and dereferenced, causing crash\n"
    printf "\n"
    info registers
    printf "\n"
    backtrace 10
    printf "\n"
end

printf "=== Script loaded ===\n"
printf "Breakpoints set at:\n"
printf "  1. Kmazarin entry\n"
printf "  2. SyscallMmap\n"
printf "  3. Crash point (0x41813f8c)\n"
printf "\nStarting execution...\n\n"

continue
