# Break on the actual exception handler, not just the vector entry

target remote :1234
set architecture aarch64
file flash/mazboot.elf

set pagination off

printf "Setting breakpoint on sync_exception_handler (actual handler code)\n"

# Break on the actual handler code, not the vector table entry
break sync_exception_handler

printf "Breakpoint set. Type 'continue' to run.\n\n"
