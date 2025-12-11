# GDB script to step through RenderChar16x16 and find SP misalignment
# This version steps through instructions and checks SP at each step

# Set breakpoint at RenderChar16x16 entry
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 at PC=$pc ===\n
  print/x $sp
  set $sp_entry = $sp
  set $sp_low = $sp_entry & 0xF
  if $sp_low == 0
    echo SP is ALIGNED at entry\n
  else
    echo SP is MISALIGNED at entry! Stopping.\n
    quit 1
  end
  # Step through instructions checking SP
  set $step_count = 0
  set $max_steps = 300
  while $step_count < $max_steps
    set $step_count = $step_count + 1
    stepi
    set $sp_current = $sp
    set $sp_low = $sp_current & 0xF
    set $pc_current = $pc
    # Check if we've left the function
    if $pc_current < 0x2be5e0 || $pc_current >= 0x2be860
      echo Left RenderChar16x16 function at PC=0x$pc_current\n
      break
    end
    # Check if SP became misaligned
    if $sp_low != 0
      echo \n*** SP MISALIGNMENT DETECTED! ***\n
      printf "Step %d: PC=0x%lx SP=0x%lx (low=0x%lx) MISALIGNED!\n", $step_count, $pc_current, $sp_current, $sp_low
      printf "SP changed from 0x%lx to 0x%lx\n", $sp_entry, $sp_current
      x/30i $pc_current-0x40
      backtrace
      echo \nStopping for inspection...\n
      quit 1
    end
    # Print every 10 steps for progress
    if ($step_count % 10) == 0
      printf "Step %d: PC=0x%lx SP=0x%lx OK\n", $step_count, $pc_current, $sp_current
    end
  end
  if $step_count >= $max_steps
    echo Reached max step count without finding misalignment\n
  end
  quit 0
end

echo \n=== Step Trace Script Loaded ===\n
echo Will step through RenderChar16x16 checking SP alignment\n
echo Run 'continue' to start\n
