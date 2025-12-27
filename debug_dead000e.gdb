# GDB Script to Debug 0xDEAD000E Page Fault
# Usage: gdb -x debug_dead000e.gdb

# Connect to QEMU
target remote :1234

# Set architecture
set architecture aarch64

# Load symbols
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Break on page fault handler
break HandlePageFault

# When we hit HandlePageFault, examine the fault
commands
  silent
  printf "=== PAGE FAULT ===\n"
  printf "Fault Address (arg0): 0x%lx\n", $x0
  printf "Fault Status (arg1):  0x%lx\n", $x1

  # Check if this is the DEAD000E fault
  if $x0 == 0xDEAD000E || $x0 == 0x0DEAD000E
    printf "\n*** CAUGHT 0xDEAD000E FAULT! ***\n\n"

    # Print ALL registers to see which ones have DEAD values
    printf "=== REGISTER DUMP ===\n"
    info registers

    # Print exception frame on stack (if accessible)
    printf "\n=== EXCEPTION FRAME (SP_EL1) ===\n"
    printf "SP_EL1: 0x%lx\n", $sp
    x/40gx $sp

    # Get backtrace to see call chain
    printf "\n=== BACKTRACE ===\n"
    backtrace 20

    # Try to find where X14 was set to DEAD000E
    printf "\n=== SEARCHING FOR X14 USAGE ===\n"
    printf "Current X14: 0x%lx\n", $x14

    # Stop for manual investigation
    printf "\nStopping for manual investigation...\n"
    printf "Commands to try:\n"
    printf "  - 'info registers' to see all registers\n"
    printf "  - 'bt' for backtrace\n"
    printf "  - 'x/10gx $sp' to examine stack\n"
    printf "  - 'disassemble' to see current code\n"
    printf "  - 'continue' to resume\n"
  else
    # Not the fault we're looking for, continue
    continue
  end
end

# Also break on exception handler entry
break sync_exception_el1
commands
  silent
  printf "Exception: FAR=0x%lx ESR=0x%lx ELR=0x%lx\n", $x0, $x1, $x2

  # Check for DEAD000E
  if $x0 == 0xDEAD000E || $x0 == 0x0DEAD000E
    printf "*** DEAD000E exception at entry! ***\n"
    info registers
  end
  continue
end

printf "GDB script loaded. Starting QEMU...\n"
printf "Will break on HandlePageFault with fault address analysis.\n"
printf "Type 'continue' to start.\n"
