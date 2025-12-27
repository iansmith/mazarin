# GDB script to catch "SVC=" being written to UART
# This sets a watchpoint on the UART data register and tracks the sequence of characters
#
# Usage: gdb -x debug_catch_svc.gdb flash/mazboot.elf
#        (gdb) target remote :1234
#        (gdb) continue

# UART0 base address for virt machine
set $uart_base = 0x09000000

# Watchpoint on UART data register to catch all writes
watch *($uart_base)

# Track last 4 characters written to detect "SVC=" pattern
set $char1 = 0
set $char2 = 0
set $char3 = 0
set $char4 = 0

# Define command to run when watchpoint hits
commands
    silent

    # Get the character being written
    set $newchar = *(unsigned char *)($uart_base)

    # Shift the sequence
    set $char1 = $char2
    set $char2 = $char3
    set $char3 = $char4
    set $char4 = $newchar

    # Check for "SVC=" pattern (0x53, 0x56, 0x43, 0x3D)
    if $char1 == 0x53 && $char2 == 0x56 && $char3 == 0x43 && $char4 == 0x3D
        printf "\n\n*** CAUGHT SVC= PATTERN! ***\n"
        printf "Character sequence: S(0x53) V(0x56) C(0x43) =(0x3D)\n\n"

        # Show backtrace
        printf "Backtrace:\n"
        bt

        # Show registers
        printf "\nRegisters:\n"
        info registers

        # Show where we are in code
        printf "\nCurrent location:\n"
        list

        # Stop execution for investigation
        printf "\nExecution stopped. Type 'continue' to resume.\n"

        # Don't auto-continue - stop here
    else
        # For debugging: print every character to see what's being written
        # Uncomment if you want to see all UART output
        # printf "%c", $newchar

        # Auto-continue for non-matching characters
        continue
    end
end

# Additional breakpoint on uartPutcDirect to catch Go function calls
break main.uartPutcDirect
commands
    silent
    set $go_char = $x0

    # Check if this is part of "SVC=" sequence
    # Shift and check
    set $char1 = $char2
    set $char2 = $char3
    set $char3 = $char4
    set $char4 = $go_char

    if $char1 == 0x53 && $char2 == 0x56 && $char3 == 0x43 && $char4 == 0x3D
        printf "\n\n*** CAUGHT SVC= PATTERN via uartPutcDirect! ***\n"
        printf "Character: %c (0x%02x)\n", $go_char, $go_char
        printf "\nBacktrace:\n"
        bt
        printf "\nRegisters:\n"
        info registers
        printf "\nExecution stopped.\n"
    else
        continue
    end
end

# Also break on uartPutsDirect to catch string writes
break main.uartPutsDirect
commands
    silent
    set $str_ptr = $x0

    # Try to read the string (first 10 characters)
    set $str_preview = (char *)$str_ptr

    # Check if string contains "SVC="
    if $_regex($str_preview, "SVC=")
        printf "\n\n*** CAUGHT SVC= in string via uartPutsDirect! ***\n"
        printf "String: %s\n", $str_preview
        printf "\nBacktrace:\n"
        bt
        printf "\nRegisters:\n"
        info registers
        printf "\nExecution stopped.\n"
    else
        continue
    end
end

printf "\n=== SVC= Detection Script Loaded ===\n"
printf "This script will catch any code that writes 'SVC=' to UART\n"
printf "\nWatchpoints and breakpoints set:\n"
printf "  - UART data register watchpoint: 0x%lx\n", $uart_base
printf "  - Breakpoint on main.uartPutcDirect\n"
printf "  - Breakpoint on main.uartPutsDirect\n"
printf "\nConnect to QEMU with: target remote :1234\n"
printf "Then type 'continue' to start execution\n"
printf "===================================\n\n"
