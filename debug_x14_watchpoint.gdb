# GDB Script to Watch X14 Register Usage
# This script watches for X14 being loaded with 0xDEAD000E and then used as a pointer

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Break when kmazarin starts executing
break *0x41800000
commands
  silent
  printf "=== Kmazarin entry point reached ===\n"
  printf "Setting watchpoint for memory access to 0xDEAD000E range...\n"

  # Set a hardware watchpoint for any memory access in the DEAD000E region
  # This will catch both reads and writes
  watch *0xDEAD000E

  printf "Watchpoint set. Continuing...\n"
  continue
end

# Alternative: Break on first instruction that uses [x14] as memory operand
# This requires knowing where kmazarin code is, so we'll use a heuristic

# Break on common pointer dereference patterns after X14 is set
# Strategy: Single-step through kmazarin initialization and watch for:
# - ldr/str instructions using x14 as base register
# - Memory operations with x14 in address calculation

printf "GDB script loaded.\n"
printf "Strategy: Watch for memory access to 0xDEAD000E\n"
printf "Type 'continue' to start.\n"
