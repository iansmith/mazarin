# GDB Script: Show ALL sync exceptions to see what's happening
# This will help us understand if exceptions are occurring and what the fault addresses are

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Disable pagination
set pagination off

printf "=================================================================\n"
printf "  Showing ALL synchronous exceptions\n"
printf "=================================================================\n\n"

# Break on sync exception handler - show EVERYTHING
break sync_exception_el1
commands
  silent
  printf "\n=== SYNC EXCEPTION ===\n"
  printf "FAR_EL1 (fault addr): 0x%016lx\n", $x0
  printf "ESR_EL1 (syndrome):   0x%016lx\n", $x1
  printf "ELR_EL1 (PC):         0x%016lx\n", $x2
  printf "SPSR_EL1:             0x%016lx\n", $x3

  # Check if this is the DEAD000E fault
  if ($x0 & 0xFFFFFFFF) == 0xDEAD000E
    printf "\n*** THIS IS THE 0xDEAD000E FAULT! ***\n"
    printf "Stopping for investigation...\n\n"
    # Don't continue - let user investigate
  else
    printf "Continuing...\n"
    continue
  end
end

# Also break on HandlePageFault
break HandlePageFault
commands
  silent
  printf "\n=== PAGE FAULT HANDLER ===\n"
  printf "Fault Address: 0x%016lx\n", $x0
  printf "Fault Status:  0x%016lx\n", $x1

  if ($x0 & 0xFFFFFFFF) == 0xDEAD000E
    printf "\n*** THIS IS THE 0xDEAD000E PAGE FAULT! ***\n"
    printf "Stopping for investigation...\n\n"
    printf "Register state:\n"
    info registers
    printf "\nBacktrace:\n"
    backtrace 20
  else
    printf "Continuing...\n"
    continue
  end
end

printf "Script loaded. Type 'continue' to run.\n"
printf "Will show ALL exceptions and page faults.\n\n"
