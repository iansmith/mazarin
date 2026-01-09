# GDB script to debug StartupParams memory corruption
# This sets breakpoints at key locations to examine memory

target remote :1234
set architecture aarch64
set pagination off
set confirm off

# Define helper command to check StartupParams
define check_startupparams
  printf "\n=== StartupParams Memory Dump ===\n"
  printf "Address: 0xFFFFFFFF41975020\n"
  printf "First 64 bytes (should contain RuntimeConfig):\n"
  x/16xw 0xFFFFFFFF41975020
  printf "\nMagic field (should be 0x4B4D5A52):\n"
  x/1xw 0xFFFFFFFF41975020
  printf "KernelHeapStart offset +104 (should be 0xFFFFFFFF41A00000):\n"
  x/1xg 0xFFFFFFFF41975020+104
  printf "KernelHeapEnd offset +112 (should be 0xFFFFFFFF45800000):\n"
  x/1xg 0xFFFFFFFF41975020+112
  printf "\n"
end

# We need to break after MMU is enabled to access high memory
# Let's break at a symbol we know exists
# Try breaking at main.loadAndRunKmazarin or after it

echo \n=== Setting up breakpoints ===\n

# First, let the system boot and MMU initialize
# We'll break manually or use a symbol

# Show available functions (for debugging)
echo Available functions to break on:\n
info functions loadAndRun
info functions populate

echo \n=== Instructions ===\n
echo 1. Type 'continue' to start execution\n
echo 2. Watch terminal 3 for serial output\n
echo 3. When you see "Verify: M=0x4B4D5A52", press Ctrl+C here\n
echo 4. Type 'check_startupparams' to examine memory\n
echo 5. Type 'continue' again\n
echo 6. When you see "Jumping to kmazarin", press Ctrl+C\n
echo 7. Type 'check_startupparams' again to compare\n
echo 8. If memory is still good, continue and break again after first mmap\n
echo \nType 'continue' to begin...\n
