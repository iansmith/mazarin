# GDB script to trace syscall return path for register corruption
# Usage: gdb -x debug_register_corruption.gdb build/mazboot/mazboot.elf

# Connect to QEMU
target remote :1234

# Load symbol file
file build/mazboot/mazboot.elf

# Break at main.SyscallMmap entry - record initial state
break main.SyscallMmap
commands
  silent
  printf "\n=== main.SyscallMmap Entry ===\n"
  printf "X0 (addr):   0x%016lx\n", $x0
  printf "X1 (len):    0x%016lx\n", $x1
  printf "W2 (prot):   0x%08x\n", $w2
  printf "W3 (flags):  0x%08x\n", $w3
  printf "W4 (fd):     %d\n", $w4
  printf "W5 (offset): 0x%08x\n", $w5
  printf "LR:          0x%016lx\n", $lr
  printf "SP:          0x%016lx\n", $sp
  finish
  printf "\n=== main.SyscallMmap Return ===\n"
  printf "X0 (result): 0x%016lx\n", $x0
  continue
end

# Break at syscall_return in exceptions.s (before ERET)
# This is where we return from EL1 to kmazarin
# Address needs to be found with: objdump -d build/mazboot/mazboot.elf | grep syscall_return
# For now, use a placeholder - user needs to find actual address
break syscall_return
commands
  silent
  printf "\n=== syscall_return (before ERET) ===\n"
  printf "X0 (return):  0x%016lx\n", $x0
  printf "X1:           0x%016lx\n", $x1
  printf "X2:           0x%016lx\n", $x2
  printf "X3:           0x%016lx\n", $x3
  printf "X4:           0x%016lx\n", $x4
  printf "X5:           0x%016lx\n", $x5
  printf "ELR_EL1:      0x%016lx (return address)\n", $ELR_EL1
  printf "SP_EL0:       0x%016lx\n", $SP_EL0
  printf "SP_EL1:       0x%016lx\n", $SP_EL1
  printf "SPSR_EL1:     0x%016lx\n", $SPSR_EL1
  # Single step through ERET
  stepi
  printf "\n=== After ERET (back in kmazarin) ===\n"
  printf "X0 (should be return value): 0x%016lx\n", $x0
  printf "PC: 0x%016lx\n", $pc
  continue
end

# Break at kmazarin's sysMmap after SVC instruction
# This is in kmazarin's code, so we need to use kmazarin.elf symbols
# Address: runtime·sysMmap+XX where XX is offset after SVC
# User needs to find with: objdump -d src/kmazarin/build/kmazarin.elf | grep -A20 "runtime.sysMmap"
# Placeholder for now
# break *0x41800XXX
# commands
#   silent
#   printf "\n=== kmazarin sysMmap after SVC ===\n"
#   printf "X0 (syscall result): 0x%016lx\n", $x0
#   continue
# end

printf "GDB script loaded.\n"
printf "NOTE: You need to find the actual addresses for:\n"
printf "  1. syscall_return in exceptions.s\n"
printf "  2. runtime.sysMmap in kmazarin.elf\n"
printf "\nFind these with:\n"
printf "  objdump -d build/mazboot/mazboot.elf | grep syscall_return\n"
printf "  objdump -d src/kmazarin/build/kmazarin.elf | grep -A20 'runtime.sysMmap'\n"
printf "\nStarting execution...\n\n"

continue
