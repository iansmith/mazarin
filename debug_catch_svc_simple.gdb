# Simplified GDB script to catch "SVC=" being written to UART
# This version uses ONLY breakpoints (no watchpoints on MMIO)
#
# Usage:
#   Terminal 1: NOGRAPHIC=1 DEBUG=1 bin/run-mazboot
#   Terminal 2: ~/mazzy/bin/target-gdb -x debug_catch_svc_simple.gdb flash/mazboot.elf
#              (gdb) target remote :1234
#              (gdb) c

# Track sequence of last 4 characters
set $char1 = 0
set $char2 = 0
set $char3 = 0
set $char4 = 0

# Breakpoint on uartPutcDirect - print every character and check for pattern
break main.uartPutcDirect
commands
    silent
    set $c = (unsigned char)$x0

    # Shift sequence
    set $char1 = $char2
    set $char2 = $char3
    set $char3 = $char4
    set $char4 = $c

    # Print character for visibility
    printf "%c", $c

    # Check for "SVC=" pattern
    if $char1 == 0x53 && $char2 == 0x56 && $char3 == 0x43 && $char4 == 0x3D
        printf "\n\n*** DETECTED SVC= PATTERN ***\n"
        printf "Last 4 chars: '%c' '%c' '%c' '%c'\n", $char1, $char2, $char3, $char4
        printf "\nBacktrace:\n"
        bt 10
        printf "\nStopping for investigation. Type 'c' to continue.\n\n"
        # Don't continue - stop here!
    else
        continue
    end
end

# Breakpoint on uartPutsDirect - check if string contains "SVC"
break main.uartPutsDirect
commands
    silent
    set $str = (char *)$x0

    # Print first few chars of string for visibility
    # Use printf with explicit format to avoid issues
    printf "[STR]"

    # Check first 20 chars for "SVC" pattern (be careful with memory access)
    set $i = 0
    set $found_s = 0
    set $found_v = 0
    set $found_c = 0
    set $found_eq = 0

    while $i < 20
        set $ch = *($str + $i)
        if $ch == 0
            loop_break
        end

        # Simple state machine to detect S->V->C->= sequence
        if $ch == 'S' || $ch == 's'
            set $found_s = 1
            set $found_v = 0
            set $found_c = 0
            set $found_eq = 0
        end
        if $found_s == 1 && ($ch == 'V' || $ch == 'v')
            set $found_v = 1
        end
        if $found_v == 1 && ($ch == 'C' || $ch == 'c')
            set $found_c = 1
        end
        if $found_c == 1 && $ch == '='
            set $found_eq = 1
            loop_break
        end

        set $i = $i + 1
    end

    if $found_eq == 1
        printf "\n\n*** DETECTED SVC= IN STRING ***\n"
        printf "String pointer: 0x%lx\n", $str
        printf "\nBacktrace:\n"
        bt 10
        printf "\nStopping for investigation.\n\n"
        # Don't continue - stop here!
    else
        continue
    end
end

# Breakpoint on print_hex64 to catch hex output after potential SVC= text
break print_hex64
commands
    silent
    # Check if we just printed "SVC=" (last 4 chars)
    if $char1 == 0x53 && $char2 == 0x56 && $char3 == 0x43 && $char4 == 0x3D
        printf "\n\n*** print_hex64 called right after SVC= ***\n"
        printf "Value to print: 0x%016lx\n", $x0
        printf "\nBacktrace:\n"
        bt 10
        printf "\nStopping for investigation.\n\n"
        # Don't continue - stop here!
    else
        continue
    end
end

printf "\n=== Simplified SVC= Detection Script ===\n"
printf "Breakpoints set on:\n"
printf "  - main.uartPutcDirect (will echo all output)\n"
printf "  - main.uartPutsDirect (checks strings)\n"
printf "  - print_hex64 (checks if called after SVC=)\n"
printf "\nConnect with: target remote :1234\n"
printf "Then type: c\n"
printf "=====================================\n\n"
