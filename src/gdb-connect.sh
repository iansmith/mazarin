#!/bin/bash

# Script to connect GDB to QEMU running in debug mode
# Usage: ./gdb-connect.sh [gdb-port]

GDB_PORT="${1:-1234}"
KERNEL_ELF="kernel.elf"

# Check if kernel.elf exists
if [ ! -f "$KERNEL_ELF" ]; then
    echo "Error: $KERNEL_ELF not found. Run 'make' first." >&2
    exit 1
fi

# Use the target GDB from the toolchain
GDB="/Users/iansmith/mazzy/bin/target-gdb"

if [ ! -f "$GDB" ]; then
    echo "Error: GDB not found at $GDB" >&2
    exit 1
fi

echo "Connecting GDB to QEMU on port $GDB_PORT..."
echo "Kernel: $KERNEL_ELF"
echo ""

# Start GDB and connect
exec "$GDB" "$KERNEL_ELF" -ex "target remote localhost:$GDB_PORT" \
    -ex "set architecture aarch64" \
    -ex "layout asm" \
    -ex "layout regs"










