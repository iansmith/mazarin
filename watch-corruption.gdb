# Watch for writes to 0x419a39a8 (the location that gets 0xDEAD000E)

# Load our break-and-watch tool
source break-and-watch.gdb

# Connect to QEMU
target remote :1234

# Load symbols
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Set architecture
set architecture aarch64

# Set a watchpoint on globalAlloc.persistent.base (0x419a1068)
# This will stop execution whenever anything writes to it
watch *0x419a1068

printf "=== Watchpoint set on 0x419a1068 (globalAlloc.persistent.base) ===\n"
printf "Will stop when anything writes to this address\n"
printf "Starting execution...\n\n"

# Automatically continue
continue
