# Simplified GDB script for SP misalignment debugging
# Uses step tracing through RenderChar16x16 to catch when SP becomes misaligned
#
# Usage:
#   1. Start QEMU: cd /Users/iansmith/mazzy && docker/mazboot -g
#   2. In another terminal: cd /Users/iansmith/mazzy/src && ./gdb-connect.sh
#   3. In GDB: source debug-sp-simple.gdb
#   4. Continue: (gdb) continue
#   5. When it stops at RenderChar16x16, use: (gdb) step-trace-sp

# Helper to check SP alignment
define check_sp
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "OK\n"
  else
    printf "MISALIGNED!\n"
    echo Stopping for inspection...
    return 1
  end
  return 0
end

# Step trace through RenderChar16x16 checking SP at each step
define step-trace-sp
  set $max_steps = 200
  set $step_count = 0
  while $step_count < $max_steps
    set $step_count = $step_count + 1
    stepi
    if check_sp() != 0
      # SP is misaligned, stop
      break
    end
    # Check if we've left the function (PC outside range)
    set $pc_val = $pc
    if $pc_val < 0x2be5e0 || $pc_val >= 0x2be860
      echo Left RenderChar16x16 function
      break
    end
  end
  if $step_count >= $max_steps
    echo Reached max step count
  end
end

# Set breakpoint at RenderChar16x16 entry
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 ===\n
  check_sp
  echo \nUse 'step-trace-sp' to step through and check SP alignment\n
  echo Or use 'continue' to continue normally\n
end

# Set breakpoint after bitmap access
break *0x2be630
commands
  echo \n=== After bitmap access ===\n
  if check_sp() != 0
    # SP is misaligned, stop here
    echo \nSP is MISALIGNED! Stopping for inspection.\n
    x/20i $pc-0x30
    backtrace
  else
    continue
  end
end

echo \n=== Simple SP Debug Script Loaded ===\n
echo Breakpoint set at RenderChar16x16 entry\n
echo After hitting the breakpoint, use:\n
echo   (gdb) step-trace-sp    # Step through checking SP at each instruction\n
echo   (gdb) continue         # Continue normally\n
echo   (gdb) stepi            # Step one instruction\n
echo \nRun 'continue' to start execution\n
