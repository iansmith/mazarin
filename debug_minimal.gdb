# Absolute minimal GDB script - no commands, no silence, just a breakpoint

target remote :1234
set architecture aarch64
file flash/mazboot.elf

# Just one breakpoint at boot entry - no commands at all
break *0x40100000

printf "Ready. Type 'continue' and you should hit breakpoint at 0x40100000\n"
