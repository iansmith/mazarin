# Ultra-simple GDB script - just log UART output to see where SVC= comes from
# This won't auto-continue, so you can step through manually
#
# Usage:
#   Terminal 1: NOGRAPHIC=1 DEBUG=1 bin/run-mazboot
#   Terminal 2: ~/mazzy/bin/target-gdb -x debug_uart_trace.gdb flash/mazboot.elf
#              (gdb) target remote :1234
#              (gdb) c

# Simple counter
set $uart_call_count = 0

# Breakpoint on uartPutcDirect - just print character and continue
break main.uartPutcDirect
commands
    silent
    set $uart_call_count = $uart_call_count + 1
    set $c = (unsigned char)$x0
    printf "%c", $c
    continue
end

# Breakpoint on uartPutsDirect - print string info and continue
break main.uartPutsDirect
commands
    silent
    set $uart_call_count = $uart_call_count + 1
    set $str = (char *)$x0
    printf "[STRING]"
    continue
end

printf "\n=== UART Trace Mode ===\n"
printf "This will print all UART output as it happens.\n"
printf "Look for 'SVC=' in the output stream.\n"
printf "\nConnect with: target remote :1234\n"
printf "Then type: c\n"
printf "Press Ctrl+C to stop tracing.\n"
printf "========================\n\n"
