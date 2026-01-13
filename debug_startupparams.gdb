# GDB script to debug StartupParams memory corruption

# Connect to QEMU
target remote :1234

# Set architecture
set architecture aarch64

# Disable pagination for cleaner output
set pagination off
set confirm off

# Continue until MMU is enabled and high memory is accessible
# We'll set the watchpoint after continuing a bit
continue

# At this point, if we hit an error, the user can manually:
# 1. Set breakpoint at populateRuntimeConfig
# 2. Continue
# 3. Set watchpoint on the address shown in "Verify:" output
