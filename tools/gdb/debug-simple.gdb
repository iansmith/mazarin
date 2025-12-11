set architecture aarch64
target remote localhost:1234
# Interrupt execution to set breakpoints
monitor stop
break *0x2be868
commands
  printf "\n=== CRASH LOCATION BREAKPOINT HIT ===\n"
  printf "PC=0x%lx\n", $pc
  printf "SP=0x%lx\n", $sp
  set $sp_val = $sp
  set $sp_low = $sp_val & 0xF
  printf "SP alignment check: low=0x%lx\n", $sp_low
  set $target = $sp_val + 34
  printf "Target address [sp+34] = 0x%lx\n", $target
  set $target_low = $target & 0xF
  printf "Target alignment: low=0x%lx\n", $target_low
  x/15i $pc-0x30
  info registers
  quit
end
monitor continue
continue
