# GDB script using hardware watchpoint on SP (if supported)
# This uses a watchpoint that only triggers when PC is in relevant functions
#
# Usage:
#   1. Start QEMU: cd /Users/iansmith/mazzy && docker/mazboot -g
#   2. In another terminal: cd /Users/iansmith/mazzy/src && ./gdb-connect.sh
#   3. In GDB: source debug-sp-watchpoint-hw.gdb
#   4. Continue: (gdb) continue

# First, set a breakpoint to enable the watchpoint only when we're in the call chain
break main.FramebufferPuts
commands
  echo \n=== Entered framebuffer call chain, enabling SP watchpoint ===\n
  # Try to set a hardware watchpoint on SP
  # Note: This may not work on all targets - if it fails, use debug-sp-simple.gdb instead
  watch -l $sp
  condition $bpnum (($sp & 0xF) != 0)
  commands
    echo \n*** SP MISALIGNMENT DETECTED! ***\n
    print/x $sp
    print/x $sp & 0xF
    print/x $pc
    # Check if we're in a relevant function
    set $pc_val = $pc
    if $pc_val >= 0x2be5e0 && $pc_val < 0x2beeb0
      echo We are in framebuffer rendering function range\n
      x/20i $pc-0x30
      backtrace
    else
      echo We are NOT in framebuffer rendering function range - ignoring\n
      continue
    end
  end
  continue
end

# Also set breakpoint at RenderChar16x16 to check alignment
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 ===\n
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP=0x%lx (low=0x%lx) ", $sp_val, $sp_low
  if $sp_low == 0
    printf "ALIGNED\n"
  else
    printf "MISALIGNED - stopping!\n"
    x/20i $pc-0x30
    backtrace
    # Don't continue - stop here
  end
  continue
end

echo \n=== Hardware Watchpoint Script Loaded ===\n
echo Note: Hardware watchpoints may not be supported on all targets.\n
echo If watchpoint fails, use debug-sp-simple.gdb instead.\n
echo \nRun 'continue' to start execution\n
