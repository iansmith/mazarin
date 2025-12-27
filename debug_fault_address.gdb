# GDB Script: Break at the exact faulting instruction
# We know from running the system that the fault occurs at ELR=0x41813F90

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Disable pagination
set pagination off
set print pretty on

printf "=================================================================\n"
printf "  Breaking at faulting instruction: 0x41813F90\n"
printf "=================================================================\n\n"

# Break at the exact faulting instruction
break *0x41813F90
commands
  silent
  printf "\n╔═══════════════════════════════════════════════════════════════╗\n"
  printf "║  STOPPED AT FAULTING INSTRUCTION (0x41813F90)                ║\n"
  printf "╚═══════════════════════════════════════════════════════════════╝\n\n"

  printf "=== REGISTER STATE (BEFORE FAULT) ===\n"
  printf "X0  = 0x%016lx   X1  = 0x%016lx\n", $x0, $x1
  printf "X2  = 0x%016lx   X3  = 0x%016lx\n", $x2, $x3
  printf "X4  = 0x%016lx   X5  = 0x%016lx\n", $x4, $x5
  printf "X6  = 0x%016lx   X7  = 0x%016lx\n", $x6, $x7
  printf "X8  = 0x%016lx   X9  = 0x%016lx\n", $x8, $x9
  printf "X10 = 0x%016lx   X11 = 0x%016lx\n", $x10, $x11
  printf "X12 = 0x%016lx   X13 = 0x%016lx\n", $x12, $x13
  printf "X14 = 0x%016lx   X15 = 0x%016lx ← X14 should be DEAD000E!\n", $x14, $x15
  printf "X16 = 0x%016lx   X17 = 0x%016lx\n", $x16, $x17
  printf "X18 = 0x%016lx   X19 = 0x%016lx\n", $x18, $x19
  printf "X20 = 0x%016lx   X21 = 0x%016lx\n", $x20, $x21
  printf "X22 = 0x%016lx   X23 = 0x%016lx\n", $x22, $x23
  printf "X24 = 0x%016lx   X25 = 0x%016lx\n", $x24, $x25
  printf "X26 = 0x%016lx   X27 = 0x%016lx\n", $x26, $x27
  printf "X28 = 0x%016lx (g)  X29 = 0x%016lx (FP)\n", $x28, $x29
  printf "X30 = 0x%016lx (LR)  SP  = 0x%016lx\n", $x30, $sp
  printf "PC  = 0x%016lx\n", $pc

  printf "\n=== FAULTING INSTRUCTION ===\n"
  x/i $pc
  printf "\n=== DISASSEMBLY AROUND FAULT ===\n"
  disassemble $pc-32, $pc+64

  printf "\n=== FUNCTION IDENTIFICATION ===\n"
  info symbol $pc

  printf "\n=== BACKTRACE ===\n"
  backtrace 20

  printf "\n=== STACK CONTENTS (top 16 words) ===\n"
  x/16gx $sp

  printf "\n=== ANALYSIS ===\n"
  printf "The instruction above is about to execute and will fault.\n"
  printf "Look at the disassembly to see which register is being dereferenced.\n"
  printf "Expected: An instruction using X14 as a base address.\n"
  printf "\n"
  printf "Commands to investigate:\n"
  printf "  si          - Single step (will trigger the fault)\n"
  printf "  stepi       - Same as si\n"
  printf "  x/10gx $x14 - Try to read from X14 (will fail - it's 0xDEAD000E)\n"
  printf "  frame 1     - Look at caller frame\n"
  printf "  list        - Show source code if available\n"
  printf "\n"
end

# Also set a catchpoint for when kmazarin starts
break *0x41800000
commands
  silent
  printf "=== Kmazarin entry point reached (0x41800000) ===\n"
  printf "Continuing to fault at 0x41813F90...\n\n"
  continue
end

printf "Script loaded. Type 'continue' to run.\n"
printf "Will break at 0x41813F90 (the faulting instruction).\n\n"
