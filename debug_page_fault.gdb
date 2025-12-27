# GDB script to debug demand paging for address 0x48000000
# Usage: gdb -x debug_page_fault.gdb build/mazboot/mazboot.elf

# Connect to QEMU (assumes QEMU running with -s -S flags)
target remote :1234

# Load symbol file
file build/mazboot/mazboot.elf

# Break at main.HandlePageFault entry (Go function name includes package)
break main.HandlePageFault
commands
  silent
  printf "\n=== main.HandlePageFault called ===\n"
  printf "Fault Address: 0x%lx\n", $x0
  printf "Fault Status:  0x%lx\n", $x1
  continue
end

# Break at main.SyscallMmap entry
break main.SyscallMmap
commands
  silent
  printf "\n=== main.SyscallMmap called ===\n"
  printf "addr:  0x%lx\n", $x0
  printf "len:   0x%lx\n", $x1
  printf "prot:  0x%x\n", $w2
  printf "flags: 0x%x\n", $w3
  printf "fd:    %d\n", $w4
  printf "offset: 0x%x\n", $w5
  # Use finish to see return value
  finish
  printf "main.SyscallMmap returning: 0x%lx\n", $x0
  # Check if this is an error (negative value or errno)
  if $x0 < 0
    printf "  -> ERROR: errno = %ld\n", -$x0
  else
    if $x0 < 4096
      printf "  -> ERROR: small errno = %ld\n", $x0
    else
      printf "  -> SUCCESS: address = 0x%lx\n", $x0
    end
  end
  continue
end

# Set initial breakpoint at kernel main
break main.KernelMain
commands
  silent
  printf "\n=== Mazboot main.KernelMain started ===\n"
  continue
end

printf "GDB script loaded. Starting execution...\n"
printf "This will trace:\n"
printf "  - SyscallMmap calls and return values\n"
printf "  - HandlePageFault calls (demand paging)\n"
printf "  - Page mapping operations\n\n"

# Start execution
continue
