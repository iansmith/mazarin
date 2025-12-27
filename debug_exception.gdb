# GDB script to catch sync exceptions
# Since the fault triggers an exception, let's catch that

target remote :1234
set architecture aarch64
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

set pagination off

printf "Setting breakpoint at sync exception handler\n"

# From exceptions.s - the sync exception handler
break sync_exception_el1

printf "Breakpoint set. Type 'continue' to run.\n"
printf "Will stop when ANY sync exception occurs.\n\n"
