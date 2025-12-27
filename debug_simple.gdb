# Simplest possible GDB script - just prove breakpoints work

target remote :1234
set architecture aarch64

printf "Connected to QEMU\n"
printf "Setting breakpoint at boot entry (0x40100000)\n"

# Break at very first instruction (boot.s entry point)
break *0x40100000

printf "Breakpoint set. Type 'continue' to start.\n"
