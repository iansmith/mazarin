# GDB script for batch mode - finds SP misalignment and stops
# This version is designed to run non-interactively and output results
#
# Usage:
#   cd /Users/iansmith/mazzy/src
#   source ../enable-mazzy
#   /Users/iansmith/mazzy/bin/target-gdb ../docker/builtin/kernel.elf \
#     -ex "target remote localhost:1234" \
#     -ex "set architecture aarch64" \
#     -x debug-sp-batch.gdb \
#     -ex "continue" \
#     -ex "quit"

# Set breakpoint at RenderChar16x16 entry
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 ===\n
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "ALIGNED\n"
  else
    printf "MISALIGNED!\n"
    echo Stopping - SP already misaligned at function entry!\n
    x/20i $pc-0x30
    backtrace
    quit 1
  end
  continue
end

# Set breakpoint after bitmap access
break *0x2be630
commands
  echo \n=== After bitmap access (0x2be630) ===\n
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "ALIGNED\n"
    continue
  else
    printf "MISALIGNED!\n"
    echo \n*** SP MISALIGNMENT DETECTED at 0x2be630! ***\n
    print/x $pc
    print/x $sp
    x/30i $pc-0x40
    backtrace
    echo \nStopping for inspection...\n
    quit 1
  end
end

# Set breakpoint at the crash location (0x2be638)
break *0x2be638
commands
  echo \n=== At instruction 0x2be638 ===\n
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "ALIGNED\n"
    continue
  else
    printf "MISALIGNED!\n"
    echo \n*** SP MISALIGNMENT DETECTED at 0x2be638! ***\n
    print/x $pc
    print/x $sp
    x/30i $pc-0x40
    backtrace
    echo \nStopping for inspection...\n
    quit 1
  end
end

# Set breakpoints at intermediate points to narrow down where SP becomes misaligned
# Between 0x2be630 and 0x2be638
break *0x2be634
commands
  echo \n=== At instruction 0x2be634 ===\n
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "ALIGNED\n"
    continue
  else
    printf "MISALIGNED!\n"
    echo \n*** SP MISALIGNMENT DETECTED at 0x2be634! ***\n
    print/x $pc
    print/x $sp
    x/30i $pc-0x40
    backtrace
    quit 1
  end
end

echo \n=== Batch SP Debug Script Loaded ===\n
echo Breakpoints set to check SP alignment at key points\n
echo Will stop and quit when misalignment is detected\n
