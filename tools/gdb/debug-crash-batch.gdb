# GDB batch script to capture crash location details
# This runs non-interactively and prints all information

# Set architecture first
set architecture aarch64

# Connect to QEMU
target remote localhost:1234

# Set breakpoints (without commands first)
break main.RenderChar16x16
break *0x2be864
break *0x2be868

# Now continue and let breakpoints trigger
continue

# When breakpoint 1 hits (RenderChar16x16 entry)
# This will be handled by the breakpoint command
