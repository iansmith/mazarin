# Simple manual debugging script
# Just connect and let you break manually with Ctrl+C

target remote :1234
set architecture aarch64
set pagination off

# Helper command
define check_mem
  printf "=== StartupParams at 0xFFFFFFFF41975020 ===\n"
  x/16xw 0xFFFFFFFF41975020
  printf "First value should be: 0x4B4D5A52 (magic)\n"
  printf "At offset +104: KernelHeapStart\n"
  printf "At offset +112: KernelHeapEnd\n"
end

echo \n=========================================\n
echo Manual Debugging Instructions:\n
echo =========================================\n
echo 1. Type: continue\n
echo 2. Watch terminal 3 until you see "Verify:"\n
echo 3. Press Ctrl+C here in GDB\n
echo 4. Type: check_mem\n
echo 5. Type: continue\n
echo 6. Watch terminal 3 until you see "mmap"\n
echo 7. Press Ctrl+C here\n
echo 8. Type: check_mem\n
echo 9. Compare the values!\n
echo \n
