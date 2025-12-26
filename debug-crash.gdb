# Debug script to examine state at crash point

target remote :1234
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000
set architecture aarch64

# Break right before the crash
break *0x41813f8c

commands
    silent
    printf "\n=== About to crash ===\n"
    printf "PC = 0x%lx\n", $pc
    printf "X3 will be loaded from [X4]\n"
    printf "X4 = 0x%lx\n", $x4
    printf "Value at [X4] = 0x%lx\n", *(uint64_t*)$x4
    printf "\nThis is the value that will cause the crash!\n"
    printf "\nMemory around X4:\n"
    x/8gx $x4-32

    # Let it continue to crash
    continue
end

printf "Breakpoint set at crash location\n"
printf "Type 'continue' to run\n"
