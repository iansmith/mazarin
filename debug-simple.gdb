# Simple debug script to track mmap return value corruption
# Problem: mmap returns 0x48000000 correctly in X0, but it gets corrupted to 0xDEAD000E

# Load our break-and-watch tool
source break-and-watch.gdb

# Connect to QEMU
target remote :1234

# Load symbols
file flash/mazboot.elf
add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000

# Set architecture
set architecture aarch64

# Let's track the first mmap call
set $mmap_count = 0

# Break at syscall_return to see when mmap returns
break syscall_return
commands
    silent
    # Check if this is an mmap syscall (222) and it succeeded
    if $x8 == 222 && $x0 < 0xFFFFFFFF00000000
        set $mmap_count = $mmap_count + 1
        printf "\n=== MMAP #%d RETURNING ===\n", $mmap_count
        printf "Return value (X0) = 0x%lx\n", $x0
        printf "SP_EL0 = 0x%lx\n", $sp
        # Get the return address from exception frame (ELR at offset 256)
        set $return_addr = *(unsigned long*)($sp + 256)
        printf "Return address (ELR) = 0x%lx\n", $return_addr

        # Show the caller's stack (SP_EL0 from exception frame at offset 288)
        set $caller_sp = *(unsigned long*)($sp + 288)
        printf "Caller's SP = 0x%lx\n", $caller_sp

        # Show what kmazarin will do with X0 after eret
        printf "\nCode at return address (what kmazarin does with mmap result):\n"
        x/10i $return_addr

        # Show caller's stack
        printf "\nCaller's stack frame:\n"
        x/16gx $caller_sp

        # For the FIRST mmap, let's single-step to see where X14/X0 gets stored
        if $mmap_count == 1
            printf "\n>>> First mmap detected! Setting up detailed tracking...\n"
            printf ">>> Will continue to next mmap or error\n"
        end
    end
    continue
end

# Break at page fault handler to see what address caused the fault
break handle_data_abort
commands
    silent
    printf "\n!!! DATA ABORT DETECTED !!!\n"
    printf "Faulting address (FAR_EL1): "
    # FAR_EL1 is a system register, let's try to read it from memory
    # Actually, let's just show registers
    info registers x0 x1 x2 pc sp
    printf "\nBacktrace:\n"
    backtrace 10
    printf "\n"
    continue
end

# Let's also catch if anyone writes 0xDEAD000E to memory
# This is trickier - we'll need to catch memory writes

printf "=== Debug script loaded ===\n"
printf "Will track mmap syscalls and their return values\n"
printf "Type 'continue' to start execution\n\n"
