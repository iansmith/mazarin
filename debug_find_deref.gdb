# GDB Script: Find the Exact Instruction Dereferencing X14=0xDEAD000E
#
# This script stops at the EXACT instruction that tries to access memory at 0xDEAD000E
# and shows you the full context of what went wrong.

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Disable pagination for cleaner output
set pagination off
set print pretty on

printf "=================================================================\n"
printf "  Finding instruction that dereferences X14=0xDEAD000E\n"
printf "=================================================================\n\n"

# Strategy: Set a memory watchpoint on 0xDEAD000E
# When accessed, we'll catch it before the fault happens

# Break on sync exception handler (catches the fault)
break sync_exception_el1
commands
  silent

  # Read FAR_EL1 from exception syndrome
  # FAR is passed as first arg to exception handler
  set $fault_addr = $x0

  if $fault_addr == 0xdead000e || $fault_addr == 0x0dead000e
    printf "\n"
    printf "╔═══════════════════════════════════════════════════════════════╗\n"
    printf "║           CAUGHT: Access to 0xDEAD000E (X14 value!)          ║\n"
    printf "╚═══════════════════════════════════════════════════════════════╝\n"
    printf "\n"

    # Show faulting instruction
    printf "=== FAULTING INSTRUCTION ===\n"
    printf "PC (ELR_EL1): 0x%016lx\n", $elr_el1
    printf "\nDisassembly around fault:\n"
    disassemble $elr_el1-32, $elr_el1+32

    printf "\n=== REGISTER STATE ===\n"
    printf "X14 (contains 0xDEAD000E):  0x%016lx\n", $x14
    printf "X0-X3 (function args):      0x%016lx 0x%016lx 0x%016lx 0x%016lx\n", $x0, $x1, $x2, $x3
    printf "X4-X7 (function args):      0x%016lx 0x%016lx 0x%016lx 0x%016lx\n", $x4, $x5, $x6, $x7
    printf "X8 (indirect result):       0x%016lx\n", $x8
    printf "X19-X22 (callee-saved):     0x%016lx 0x%016lx 0x%016lx 0x%016lx\n", $x19, $x20, $x21, $x22
    printf "X28 (g pointer):            0x%016lx\n", $x28
    printf "X29 (FP):                   0x%016lx\n", $x29
    printf "X30 (LR):                   0x%016lx\n", $x30
    printf "SP:                         0x%016lx\n", $sp

    # Try to identify the function
    printf "\n=== FUNCTION IDENTIFICATION ===\n"
    info symbol $elr_el1
    printf "\nBacktrace from fault:\n"
    backtrace 15

    # Look at the exact instruction bytes
    printf "\n=== INSTRUCTION BYTES ===\n"
    printf "Bytes at PC: "
    x/4bx $elr_el1
    printf "Instruction word: "
    x/1wx $elr_el1

    # Analyze instruction to see how X14 is used
    printf "\n=== INSTRUCTION ANALYSIS ===\n"
    printf "Examining instruction at 0x%lx\n", $elr_el1
    printf "Looking for patterns like:\n"
    printf "  - ldr  xN, [x14, #offset]   (load from x14+offset)\n"
    printf "  - str  xN, [x14, #offset]   (store to x14+offset)\n"
    printf "  - ldr  xN, [x14]            (load from x14)\n"
    printf "  - ldrb wN, [x14, xM]        (load byte from x14+xM)\n"
    printf "\n"

    # Check stack for clues
    printf "=== STACK CONTENTS ===\n"
    printf "Stack pointer: 0x%016lx\n", $sp
    printf "Stack contents (16 words):\n"
    x/16gx $sp

    # Show nearby valid addresses for comparison
    printf "\n=== NEARBY VALID ADDRESSES (for comparison) ===\n"
    printf "Other registers with valid-looking addresses:\n"
    if $x0 >= 0x40000000 && $x0 < 0x50000000
      printf "  X0  = 0x%016lx (looks valid - in RAM range)\n", $x0
    end
    if $x1 >= 0x40000000 && $x1 < 0x50000000
      printf "  X1  = 0x%016lx (looks valid - in RAM range)\n", $x1
    end
    if $x2 >= 0x40000000 && $x2 < 0x50000000
      printf "  X2  = 0x%016lx (looks valid - in RAM range)\n", $x2
    end
    if $x3 >= 0x40000000 && $x3 < 0x50000000
      printf "  X3  = 0x%016lx (looks valid - in RAM range)\n", $x3
    end
    if $x19 >= 0x40000000 && $x19 < 0x50000000
      printf "  X19 = 0x%016lx (looks valid - in RAM range)\n", $x19
    end
    if $x20 >= 0x40000000 && $x20 < 0x50000000
      printf "  X20 = 0x%016lx (looks valid - in RAM range)\n", $x20
    end

    printf "\n=== NEXT STEPS ===\n"
    printf "1. Examine the disassembly to see which register is used as base\n"
    printf "2. Check if X14 should be a different register\n"
    printf "3. Trace back to see where X14 was set to 0xDEAD000E\n"
    printf "4. Check exception frame to see saved register values\n"
    printf "\n"
    printf "Commands to try:\n"
    printf "  frame 0                    - Select current frame\n"
    printf "  x/40gx $sp                 - Show more stack\n"
    printf "  info registers all         - Show ALL registers including system\n"
    printf "  disassemble                - Show more disassembly\n"
    printf "\n"
    printf "Press 'c' to continue or Ctrl-C to stop\n"

    # Don't continue automatically - let user investigate
    # continue
  else
    # Not our fault, keep going
    continue
  end
end

printf "Script loaded. Type 'continue' to run.\n"
printf "Will stop when code tries to access 0xDEAD000E.\n\n"
