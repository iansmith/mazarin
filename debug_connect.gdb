# Simple GDB connection script
target remote :1234
set architecture aarch64
set pagination off

# Continue execution
continue
