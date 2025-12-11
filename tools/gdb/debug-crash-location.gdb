# GDB script to isolate exact crash location
# Focuses on the crash at 0x2be868 (stur x0, [sp, #34])

# Set breakpoint at RenderChar16x16 entry
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 ===\n
  printf "PC=0x%lx SP=0x%lx\n", $pc, $sp
  set $sp_entry = $sp
  set $sp_low = $sp_entry & 0xF
  if $sp_low == 0
    echo SP is ALIGNED at entry\n
  else
    echo SP is MISALIGNED at entry!\n
  end
  continue
end

# Set breakpoint right before the problematic stur instruction
break *0x2be864
commands
  echo \n=== Before bitmap load (ldr x0, [x1, x0]) ===\n
  printf "PC=0x%lx SP=0x%lx\n", $pc, $sp
  set $sp_before = $sp
  set $sp_low = $sp_before & 0xF
  if $sp_low == 0
    echo SP is ALIGNED before bitmap load\n
  else
    echo SP is MISALIGNED before bitmap load!\n
  end
  x/5i $pc
  info registers x0 x1 x2 sp
  continue
end

# Set breakpoint at the exact crash location (stur instruction)
break *0x2be868
commands
  echo \n=== AT CRASH LOCATION: stur x0, [sp, #34] ===\n
  printf "PC=0x%lx SP=0x%lx\n", $pc, $sp
  set $sp_crash = $sp
  set $sp_low = $sp_crash & 0xF
  set $target_addr = $sp_crash + 34
  printf "SP=0x%lx (low=0x%lx)\n", $sp_crash, $sp_low
  printf "Target address [sp+34] = 0x%lx (low=0x%lx)\n", $target_addr, $target_addr & 0xF
  if $sp_low == 0
    echo SP is ALIGNED\n
  else
    echo SP is MISALIGNED!\n
  end
  x/10i $pc-0x20
  info registers x0 x1 x2 sp
  echo \nAbout to execute: stur x0, [sp, #34]\n
  echo This will crash if SP is misaligned or target address is misaligned\n
  stepi
end

# Set breakpoint after stur (if it doesn't crash)
break *0x2be86c
commands
  echo \n=== After stur instruction (didn't crash!) ===\n
  printf "PC=0x%lx SP=0x%lx\n", $pc, $sp
  set $sp_after = $sp
  set $sp_low = $sp_after & 0xF
  if $sp_low == 0
    echo SP is ALIGNED after stur\n
  else
    echo SP is MISALIGNED after stur!\n
  end
  x/5i $pc
  info registers x0 x1 x2 sp
  continue
end

# Catch exceptions
catch throw
commands
  echo \n=== EXCEPTION CAUGHT ===\n
  printf "PC=0x%lx SP=0x%lx\n", $pc, $sp
  x/10i $pc-0x20
  info registers
  backtrace
  echo \nStopping for inspection...\n
end

echo \n=== Crash Location Debug Script Loaded ===\n
echo Breakpoints set at:\n
echo   1. RenderChar16x16 entry\n
echo   2. Before bitmap load (0x2be864)\n
echo   3. CRASH LOCATION: stur instruction (0x2be868)\n
echo   4. After stur (if no crash)\n
echo \nRun 'continue' to start execution\n
