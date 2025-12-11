# Working GDB script for crash location debugging
# To use: Start QEMU with -g flag, then source this script

# Set architecture
set architecture aarch64

# If not already connected, connect to QEMU
# (If already connected, this will be ignored)
target remote localhost:1234

# Set breakpoint at crash location
break *0x2be868

# Define what to do when breakpoint hits
commands
printf "\n"
printf "======================================\n"
printf "=== CRASH LOCATION BREAKPOINT HIT ===\n"
printf "======================================\n"
printf "PC  = 0x%lx\n", $pc
printf "SP  = 0x%lx\n", $sp
printf "\n"
set $sp_val = $sp
set $sp_low = $sp_val & 0xF
printf "SP alignment check:\n"
printf "  SP = 0x%lx\n", $sp_val
printf "  SP & 0xF = 0x%lx ", $sp_low
if $sp_low == 0
  printf "(ALIGNED)\n"
else
  printf "(MISALIGNED!)\n"
end
printf "\n"
set $target = $sp_val + 34
set $target_low = $target & 0xF
printf "Target address calculation:\n"
printf "  [sp + 34] = 0x%lx\n", $target
printf "  Target & 0xF = 0x%lx ", $target_low
if $target_low == 0
  printf "(ALIGNED)\n"
else
  printf "(MISALIGNED!)\n"
end
printf "\n"
printf "Disassembly around crash:\n"
x/20i $pc-0x40
printf "\nRegisters:\n"
info registers x0 x1 x2 sp x28 x29 x30
printf "\nStack contents:\n"
x/10gx $sp
printf "\n"
continue
end

printf "\n=== Breakpoint set at 0x2be868 (crash location) ===\n"
printf "Run 'continue' to start execution\n"
printf "When breakpoint hits, it will show SP, target address, and register state\n\n"
