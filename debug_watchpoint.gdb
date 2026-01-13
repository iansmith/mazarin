# GDB script with hardware watchpoint
# This will catch ANY write to StartupParams

target remote :1234
set architecture aarch64
set pagination off
set confirm off

# First, we need to let the program run until high memory is mapped
# We'll continue until we hit the first data abort or similar
# Then we'll set the watchpoint

echo \n=== Initial boot - continuing to MMU setup ===\n
continue

# If we get here, something stopped us (breakpoint, signal, etc)
# Now try to set a hardware watchpoint on StartupParams
# Hardware watchpoints work differently and should be available even in QEMU

echo \n=== Attempting to set hardware watchpoint ===\n
# Watch for writes to the first word of StartupParams (the Magic field)
# Using physical address might work better
awatch *(uint32_t*)0xFFFFFFFF41975020

# If that fails, we'll need to do manual breaking
echo If watchpoint failed, use manual method:\n
echo   1. Press Ctrl+C when you see "Verify:" in serial output\n
echo   2. Run: x/16xw 0xFFFFFFFF41975020\n
echo   3. Continue and break again after "mmap" appears\n
echo   4. Run: x/16xw 0xFFFFFFFF41975020 again\n

# Continue execution - watchpoint will trigger on any write
continue
