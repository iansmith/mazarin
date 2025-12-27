# GDB script that single-steps from the start to prove GDB works

target remote :1234
set architecture aarch64
file flash/mazboot.elf

printf "Connected to QEMU\n"
printf "Current PC: "
print/x $pc

printf "\nExecuting 5 single-step instructions:\n"

# Single-step 5 instructions to prove we can control execution
stepi
printf "Step 1: PC = 0x%lx\n", $pc
stepi
printf "Step 2: PC = 0x%lx\n", $pc
stepi
printf "Step 3: PC = 0x%lx\n", $pc
stepi
printf "Step 4: PC = 0x%lx\n", $pc
stepi
printf "Step 5: PC = 0x%lx\n", $pc

printf "\nSingle-stepping works! Now setting breakpoint at KernelMain\n"

# Break at Go's KernelMain function
break main.KernelMain

printf "Breakpoint set. Type 'continue' to run to KernelMain\n"
