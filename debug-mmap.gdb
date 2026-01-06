# Debug script for tracking mmap return value corruption
# Problem: mmap returns 0x48000000 correctly, but persistent.base gets 0xDEAD000E

# Load our break-and-watch tool
source break-and-watch.gdb

# Connect to QEMU
target remote :1234

# Load symbols
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Set up to break at mmap syscall return
# We'll watch X0 to see if it gets corrupted
break SyscallMmap
commands
    silent
    printf "=== SyscallMmap called ===\n"
    printf "X0 (addr) = 0x%lx\n", $x0
    printf "X1 (length) = 0x%lx\n", $x1
    printf "X2 (prot) = 0x%lx\n", $x2
    printf "X3 (flags) = 0x%lx\n", $x3
    continue
end

# Break when mmap is about to return
# We need to find the return point
break syscall_return
commands
    silent
    # Check if this is an mmap return (syscall number 222)
    if $x8 == 222
        printf "\n=== Mmap returning ===\n"
        printf "Return value (X0) = 0x%lx\n", $x0
        printf "SP_EL0 = 0x%lx\n", $sp

        # The return value will be in X0 when we eret
        # Let's examine where the caller expects to store it
        printf "\nExamining caller's stack:\n"
        x/8gx $sp

        printf "\nPress ENTER to continue or Ctrl-C to stop...\n"
    end
    continue
end

# Let's also track all writes to the value 0xDEAD000E
# This will help us find who's writing the poison value
break *0
commands
    silent
    if $x0 == 0xDEAD000E || $x1 == 0xDEAD000E || $x2 == 0xDEAD000E
        printf "\n!!! Found 0xDEAD000E in registers !!!\n"
        printf "PC = 0x%lx\n", $pc
        printf "X0 = 0x%lx\n", $x0
        printf "X1 = 0x%lx\n", $x1
        printf "X2 = 0x%lx\n", $x2
        info registers
        backtrace
    end
    continue
end

printf "Debug script loaded. Type 'continue' to start execution.\n"
printf "Will track mmap syscall and return value...\n"
