# File: break-and-watch.gdb
#
# Purpose: Set a breakpoint at a location, then automatically set a watchpoint
#          when that breakpoint is hit.
#
# Usage:
#   (gdb) source break-and-watch.gdb
#   (gdb) break-and-watch <location> <address> [access-type]
#
# Arguments:
#   location     - Function name or address where breakpoint should be set
#   address      - Memory address to watch (can be expression like $sp+16)
#   access-type  - Optional: "write" (default), "read", or "access" (read/write)
#
# Examples:
#   break-and-watch syscall_handler 0x5F000000
#   break-and-watch *0x401d3000 $sp+16 write
#   break-and-watch exception_entry exceptionFrame.elr access

define break-and-watch
    if $argc < 2
        echo Usage: break-and-watch <location> <address> [access-type]\n
        echo   location     - Function name or address for breakpoint\n
        echo   address      - Memory address to watch\n
        echo   access-type  - "write" (default), "read", or "access"\n
        echo\n
        echo Examples:\n
        echo   break-and-watch syscall_handler 0x5F000000\n
        echo   break-and-watch *0x401d3000 \$sp+16 write\n
    else
        # Set the breakpoint
        break $arg0

        # Set commands to execute when this breakpoint hits
        commands
            silent
            printf "=== Breakpoint hit at %s ===\n", "$arg0"

            # Set the watchpoint based on access type (default to write)
            if $argc >= 3
                if strcmp("$arg2", "read") == 0
                    rwatch *$arg1
                    printf "Read-watchpoint set on address %s\n", "$arg1"
                end
                if strcmp("$arg2", "access") == 0
                    awatch *$arg1
                    printf "Access-watchpoint set on address %s\n", "$arg1"
                end
                if strcmp("$arg2", "write") == 0
                    watch *$arg1
                    printf "Write-watchpoint set on address %s\n", "$arg1"
                end
            else
                watch *$arg1
                printf "Write-watchpoint set on address %s\n", "$arg1"
            end

            printf "Continuing...\n\n"

            # Continue execution - will stop when watchpoint triggers
            continue
        end

        printf "Breakpoint set at: %s\n", "$arg0"
        printf "Will watch address: %s\n", "$arg1"
    end
end

# Convenience alias
define baw
    break-and-watch $arg0 $arg1 $arg2
end

document break-and-watch
Set a breakpoint that automatically sets a watchpoint when hit.
Useful for debugging memory corruption after a specific point.

Usage: break-and-watch <location> <address> [access-type]
Alias: baw
end
