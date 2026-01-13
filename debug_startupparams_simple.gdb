# GDB script to debug StartupParams memory corruption
# Simpler approach: set breakpoints and examine memory at key points

# Connect to QEMU
target remote :1234
set architecture aarch64
set pagination off
set confirm off

# Function to examine StartupParams memory
define check_startupparams
  printf "=== Checking StartupParams at 0xFFFFFFFF41975020 ===\n"
  x/16xw 0xFFFFFFFF41975020
  printf "Magic (first 4 bytes): "
  x/1xw 0xFFFFFFFF41975020
  printf "\n"
end

# Let the program run initially
echo Running to main...\n
continue

# From here, you can manually:
# 1. Break after seeing "Verify:" in serial output (Ctrl+C)
# 2. Run: check_startupparams
# 3. Continue and break again after "Jumping to kmazarin"
# 4. Run: check_startupparams again
# 5. Compare the values
