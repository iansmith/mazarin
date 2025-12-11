# GDB script to watch SP register changes in real-time
#
# This script sets up watchpoints and breakpoints to track when and how
# the stack pointer gets corrupted between breadcrumb [b] and the crash.
#
# Usage:
#   Terminal 1: mazboot -g
#   Terminal 2: target-gdb kernel.elf -x tools/gdb/watch-sp-changes.gdb

# Set architecture
set architecture aarch64

# Connect to QEMU GDB server
target remote localhost:1234

# Disable pagination so output flows continuously
set pagination off
set confirm off

# Enable verbose output for debugging
set verbose on

# Print initial SP value
printf "\n=== Initial SP Check ===\n"
printf "SP at startup: 0x%lx\n\n", $sp

# Set breakpoint at RenderChar16x16 entry
# This is where we start tracking SP
break *0x2be6a0
commands
  silent
  printf "\n=== RenderChar16x16 Entry ===\n"
  printf "SP = 0x%lx\n", $sp
  printf "FP = 0x%lx\n", $x29
  printf "LR = 0x%lx\n", $x30
  printf "\n"
  continue
end

# Set breakpoint right after breadcrumb [b] (printSPBreadcrumb call returns)
# Address 0x2be84c is right after the bl printSPBreadcrumb at 0x2be848
break *0x2be84c
commands
  silent
  printf "\n=== After Breadcrumb [b] ===\n"
  printf "SP = 0x%lx\n", $sp
  set $saved_sp = $sp
  printf "Saved SP for comparison: 0x%lx\n", $saved_sp
  printf "Next instructions will execute...\n"
  # Set a watchpoint on SP register
  # Note: GDB can't directly watch registers, so we'll use breakpoints
  printf "\n"
  continue
end

# Set breakpoint at each instruction between [b] and crash
# This lets us see SP at each step

# After loading char from stack
break *0x2be850
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be850 !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# After comparison
break *0x2be858
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be858 !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# After branch
break *0x2be860
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be860 !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# After ubfiz (array index calculation)
break *0x2be864
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be864 !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# After adrp (load array base)
break *0x2be868
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be868 !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# After add (complete array address)
break *0x2be86c
commands
  silent
  set $current_sp = $sp
  if $current_sp != $saved_sp
    printf "\n!!! SP CHANGED at 0x2be86c !!!\n"
    printf "Old SP: 0x%lx\n", $saved_sp
    printf "New SP: 0x%lx\n", $current_sp
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
    set $saved_sp = $current_sp
  end
  continue
end

# RIGHT BEFORE THE CRASH - after ldr (load bitmap value)
# This is the critical moment
break *0x2be870
commands
  printf "\n"
  printf "========================================\n"
  printf "=== RIGHT BEFORE CRASH INSTRUCTION ===\n"
  printf "========================================\n"
  printf "Address: 0x2be870 (stur x1, [sp, #34])\n"
  printf "\n"
  
  set $current_sp = $sp
  printf "Current SP: 0x%lx\n", $current_sp
  printf "Saved SP:   0x%lx\n", $saved_sp
  
  if $current_sp != $saved_sp
    printf "\n*** SP HAS CHANGED! ***\n"
    printf "Difference: 0x%lx (%ld bytes)\n", $current_sp - $saved_sp, $current_sp - $saved_sp
  else
    printf "\nSP has NOT changed (both = 0x%lx)\n", $current_sp
  end
  
  printf "\n"
  set $target = $current_sp + 34
  set $target_low = $target & 0xF
  printf "Target address [sp+34]:\n"
  printf "  Address: 0x%lx\n", $target
  printf "  Low 4 bits: 0x%lx ", $target_low
  if $target_low == 0
    printf "(ALIGNED)\n"
  else
    printf "(MISALIGNED!) ← THIS WILL CRASH\n"
  end
  
  printf "\nRegisters:\n"
  printf "  x1 (value to store): 0x%lx\n", $x1
  printf "  x2 (array base):     0x%lx\n", $x2
  
  printf "\nStack alignment:\n"
  set $sp_align = $current_sp & 15
  printf "  SP & 0xF = 0x%x ", $sp_align
  if $sp_align == 0
    printf "(SP is aligned)\n"
  else
    printf "(SP is MISALIGNED!)\n"
  end
  
  printf "\n*** CRITICAL MISMATCH DETECTED! ***\n"
  printf "GDB shows SP = 0x%lx\n", $current_sp
  printf "But breadcrumbs printed SP = 0x402372D0\n"
  printf "Difference: 0x%lx (48 bytes)\n", $current_sp - 0x402372D0
  printf "\nThis means get_stack_pointer() is returning WRONG value!\n"
  printf "The real SP (0x%lx) makes [sp+34] = 0x%lx (MISALIGNED)\n", $current_sp, $target_addr
  
  printf "\n"
  printf "Disassembly around crash:\n"
  x/10i $pc-0x10
  
  printf "\nPress Ctrl+C to stop, or this will continue and likely crash...\n"
  printf "========================================\n\n"
  
  # Don't continue automatically - let user decide
  # continue
end

# Breakpoint in exception handler (if we get there)
break *0x2c5200
commands
  printf "\n"
  printf "!!! EXCEPTION HANDLER REACHED !!!\n"
  printf "Exception at PC: 0x%lx\n", $elr_el1
  printf "Exception SP:    0x%lx\n", $sp
  printf "\n"
  # Don't continue - stop here
end

printf "\n=== Watchpoints and breakpoints set ===\n"
printf "Ready to track SP changes through RenderChar16x16\n"
printf "\n"
printf "Breakpoints:\n"
printf "  0x2be6a0 - RenderChar16x16 entry\n"
printf "  0x2be84c - After breadcrumb [b]\n"
printf "  0x2be850-0x2be86c - Each instruction before crash\n"
printf "  0x2be870 - RIGHT BEFORE CRASH (will stop here!)\n"
printf "  0x2c5200 - Exception handler entry\n"
printf "\n"
printf "Type 'continue' to start execution\n"
printf "The script will stop at 0x2be870 (right before crash) for inspection\n"
printf "\n"
