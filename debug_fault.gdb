# GDB script to catch the 0xDEAD000E fault
# We know breakpoints work, so this should catch the fault

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

set pagination off
set print pretty on

printf "Setting breakpoint at faulting instruction: 0x41813F90\n"

# Break at the exact address that faults (from normal run: ELR=0x41813F90)
break *0x41813F90

printf "Breakpoint set. Type 'continue' to run.\n"
printf "When it hits, we'll see which instruction dereferences X14.\n\n"
