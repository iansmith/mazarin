# Watch for writes to globalAlloc.persistent.base and wait for trigger

# Load tools
source break-and-watch.gdb

# Connect
target remote :1234
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000
set architecture aarch64

# Set watchpoint
watch *0x419a1068

# When watchpoint triggers, show detailed info
commands
    silent
    printf "\n"
    printf "============================================================\n"
    printf "WATCHPOINT TRIGGERED!\n"
    printf "Something wrote to 0x419a1068 (globalAlloc.persistent.base)\n"
    printf "============================================================\n"
    printf "\n"
    printf "PC = 0x%lx\n", $pc
    printf "\n"
    info registers
    printf "\n"
    backtrace 20
    printf "\n"
    printf "Memory at 0x419a1068:\n"
    x/4gx 0x419a1068
    printf "\n"
    # Continue to see if there are more writes
    continue
end

printf "=== Watchpoint configured ===\n"
printf "Continuing execution...\n"
printf "Will stop and show details when 0x419a1068 is written\n\n"

# Start execution
continue
