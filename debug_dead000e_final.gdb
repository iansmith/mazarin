# GDB script to catch the 0xDEAD000E fault specifically
# Now that the PCI ECAM bug is fixed, we can focus on this fault

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

set pagination off
set print pretty on

printf "=================================================================\n"
printf "  Catching 0xDEAD000E fault (X14 dereference)\n"
printf "=================================================================\n\n"

# Break on exception handler
break sync_exception_handler

commands
  silent

  # Read fault address from system register
  set $far = $FAR_EL1

  # Check if this is the DEAD000E fault
  if ($far & 0xFFFFFFFF) == 0xDEAD000E
    printf "\n╔════════════════════════════════════════════════════════════════╗\n"
    printf "║  CAUGHT: 0xDEAD000E FAULT (X14 being dereferenced!)          ║\n"
    printf "╚════════════════════════════════════════════════════════════════╝\n\n"

    printf "=== EXCEPTION INFORMATION ===\n"
    printf "FAR_EL1 (fault addr): 0x%016lx\n", $FAR_EL1
    printf "ESR_EL1 (syndrome):   0x%016lx\n", $ESR_EL1
    printf "ELR_EL1 (fault PC):   0x%016lx\n", $ELR_EL1
    printf "SPSR_EL1:             0x%016lx\n\n", $SPSR_EL1

    printf "=== FAULTING INSTRUCTION ===\n"
    printf "Address: 0x%016lx\n", $ELR_EL1
    x/i $ELR_EL1
    printf "\n"

    printf "=== DISASSEMBLY AROUND FAULT ===\n"
    disassemble $ELR_EL1-32, $ELR_EL1+64

    printf "\n=== FUNCTION IDENTIFICATION ===\n"
    info symbol $ELR_EL1

    printf "\n=== KEY REGISTERS ===\n"
    printf "X14 (should be 0xDEAD000E): 0x%016lx\n", $x14
    printf "X0-X7:\n"
    printf "  X0  = 0x%016lx   X1  = 0x%016lx\n", $x0, $x1
    printf "  X2  = 0x%016lx   X3  = 0x%016lx\n", $x2, $x3
    printf "  X4  = 0x%016lx   X5  = 0x%016lx\n", $x4, $x5
    printf "  X6  = 0x%016lx   X7  = 0x%016lx\n", $x6, $x7
    printf "X19-X22 (callee-saved):\n"
    printf "  X19 = 0x%016lx   X20 = 0x%016lx\n", $x19, $x20
    printf "  X21 = 0x%016lx   X22 = 0x%016lx\n", $x21, $x22
    printf "X28 (g), X29 (FP), X30 (LR):\n"
    printf "  X28 = 0x%016lx (g pointer)\n", $x28
    printf "  X29 = 0x%016lx (frame pointer)\n", $x29
    printf "  X30 = 0x%016lx (link register)\n", $x30
    printf "SP  = 0x%016lx\n\n", $sp

    printf "=== ALL REGISTERS (for test value check) ===\n"
    info registers

    printf "\n=== CALL STACK ===\n"
    printf "Stack pointer: 0x%016lx\n", $sp
    printf "Top 20 words of stack:\n"
    x/20gx $sp

    printf "\n╔════════════════════════════════════════════════════════════════╗\n"
    printf "║  STOPPED - Ready for investigation                            ║\n"
    printf "╚════════════════════════════════════════════════════════════════╝\n"
    printf "\nCommands to try:\n"
    printf "  x/20gx $sp        - Show more stack\n"
    printf "  info registers    - Show all registers\n"
    printf "  disassemble       - Show more code\n"
    printf "  continue          - Resume (will likely halt again)\n"
    printf "\n"

    # Don't continue - stop for investigation
  else
    # Not the fault we want, keep going
    continue
  end
end

printf "Script loaded. Type 'continue' to run.\n"
printf "Will stop when FAR_EL1 = 0xDEAD000E.\n\n"
