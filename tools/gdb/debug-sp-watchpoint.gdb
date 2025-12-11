# GDB script to debug SP misalignment in RenderChar16x16 call chain
# 
# Usage:
#   1. Start QEMU: cd /Users/iansmith/mazzy && docker/mazboot -g
#   2. In another terminal: cd /Users/iansmith/mazzy/src && ./gdb-connect.sh
#   3. In GDB: source debug-sp-watchpoint.gdb
#   4. Continue execution: (gdb) continue
#
# This script sets breakpoints at key functions and checks SP alignment.
# When SP becomes misaligned, execution will stop for inspection.

# Function addresses (from objdump -t)
# main.RenderChar16x16: 0x2be5e0
# main.RenderCharAtCursor16x16: 0x2be860
# main.FramebufferPutc16x16: 0x2bed00
# main.FramebufferPutc: 0x2bedc0
# main.FramebufferPuts: 0x2bee00

# Helper function to check and print SP alignment
define check_sp_alignment
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP = 0x%lx, low 4 bits = 0x%lx", $sp_val, $sp_low
  if $sp_low == 0
    printf " (ALIGNED - 16-byte boundary)\n"
  else
    printf " (MISALIGNED!)\n"
  end
end

# Set breakpoint at FramebufferPuts entry
break main.FramebufferPuts
commands
  echo \n=== Entered FramebufferPuts ===\n
  check_sp_alignment
  print/x $pc
  continue
end

# Set breakpoint at FramebufferPutc entry
break main.FramebufferPutc
commands
  echo \n=== Entered FramebufferPutc ===\n
  check_sp_alignment
  continue
end

# Set breakpoint at FramebufferPutc16x16 entry
break main.FramebufferPutc16x16
commands
  echo \n=== Entered FramebufferPutc16x16 ===\n
  check_sp_alignment
  continue
end

# Set breakpoint at RenderCharAtCursor16x16 entry
break main.RenderCharAtCursor16x16
commands
  echo \n=== Entered RenderCharAtCursor16x16 ===\n
  check_sp_alignment
  continue
end

# Set breakpoint at RenderChar16x16 entry
break main.RenderChar16x16
commands
  echo \n=== Entered RenderChar16x16 ===\n
  check_sp_alignment
  continue
end

# Set breakpoint after bitmap access in RenderChar16x16 (address from disassembly)
# This is after the breadcrumb [G] and bitmap access
break *0x2be630
commands
  echo \n=== After bitmap access in RenderChar16x16 ===\n
  check_sp_alignment
  if ($sp & 0xF) != 0
    echo \n*** SP MISALIGNMENT DETECTED! ***\n
    echo Stopping for inspection...\n
    # Don't continue - stop here for inspection
  else
    continue
  end
end

# Set breakpoint at key instruction addresses in RenderChar16x16
# Based on disassembly, check SP at various points
break *0x2be638
condition $bpnum ($sp & 0xF) != 0
commands
  echo \n*** SP MISALIGNMENT DETECTED at 0x2be638! ***\n
  check_sp_alignment
  print/x $pc
  x/20i $pc-0x30
  backtrace
  echo \nStopping for inspection...\n
end

# Set a breakpoint that will catch any misaligned SP in the function range
# We'll use a Python-based approach for this
python
import gdb

class SPAlignmentBreakpoint(gdb.Breakpoint):
    """Breakpoint that checks SP alignment when PC is in relevant functions"""
    
    # Function address ranges
    FUNCTION_RANGES = [
        (0x2be5e0, 0x2be860),  # RenderChar16x16
        (0x2be860, 0x2be8c0),  # RenderCharAtCursor16x16
        (0x2bed00, 0x2bedc0),  # FramebufferPutc16x16
        (0x2bedc0, 0x2bee00),  # FramebufferPutc
        (0x2bee00, 0x2beeb0),  # FramebufferPuts
    ]
    
    def __init__(self):
        # Set breakpoint at a location that will be hit frequently
        # We'll use a temporary breakpoint that we'll move
        super(SPAlignmentBreakpoint, self).__init__("*0x2be5e0", internal=True)
        self.silent = True
    
    def stop(self):
        pc = int(gdb.parse_and_eval("$pc"))
        sp = int(gdb.parse_and_eval("$sp"))
        
        # Check if we're in a relevant function
        in_range = False
        for start, end in self.FUNCTION_RANGES:
            if start <= pc < end:
                in_range = True
                break
        
        if not in_range:
            return False  # Don't stop, continue
        
        # Check SP alignment
        if (sp & 0xF) != 0:
            print("\n*** SP MISALIGNMENT DETECTED! ***")
            print("PC = 0x%x" % pc)
            print("SP = 0x%x (misaligned, low 4 bits = 0x%x)" % (sp, sp & 0xF))
            gdb.execute("x/20i $pc-0x30")
            gdb.execute("backtrace")
            return True  # Stop here
        
        return False  # Continue execution

# Create the breakpoint (but we'll use a simpler approach instead)
# sp_bp = SPAlignmentBreakpoint()
end

echo \n=== SP Watchpoint Script Loaded ===\n
echo Breakpoints set at:\n
echo  1. FramebufferPuts entry\n
echo  2. FramebufferPutc entry\n
echo  3. FramebufferPutc16x16 entry\n
echo  4. RenderCharAtCursor16x16 entry\n
echo  5. RenderChar16x16 entry\n
echo  6. After bitmap access in RenderChar16x16 (0x2be630)\n
echo  7. Conditional breakpoint at 0x2be638 (if SP misaligned)\n
echo \nAll breakpoints check SP alignment and continue unless misaligned.\n
echo Run 'continue' to start execution\n
echo \nTip: Use 'step' or 'next' to trace through code after hitting a breakpoint\n
