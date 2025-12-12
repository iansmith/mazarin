#!/bin/bash

# Script to connect GDB to QEMU running in debug mode
# Usage: ./gdb-connect.sh [gdb-port]
#
# First, start QEMU with GDB server:
#   cd /Users/iansmith/mazzy && docker/mazboot -g
#
# Then in another terminal, run this script:
#   cd /Users/iansmith/mazzy/tools/gdb && ./gdb-connect.sh

GDB_PORT="${1:-1234}"

# Use the QEMU kernel (same one that mazboot uses)
KERNEL_ELF="../../docker/builtin/kernel.elf"

# Fallback to build/mazboot/mazboot if builtin doesn't exist
if [ ! -f "$KERNEL_ELF" ]; then
    KERNEL_ELF="../../build/mazboot/mazboot"
    if [ ! -f "$KERNEL_ELF" ]; then
        echo "Error: kernel.elf not found. Expected:" >&2
        echo "  - ../../docker/builtin/kernel.elf" >&2
        echo "  - ../../build/mazboot/mazboot" >&2
        echo "Please build the QEMU kernel first: cd ../../src/mazboot && make qemu" >&2
        exit 1
    fi
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

# Get the absolute path to the source directory (src/mazboot)
SRC_DIR="$(cd "$(dirname "$0")/../../src/mazboot" && pwd)"
echo "Source directory: $SRC_DIR"
echo ""

echo "Useful GDB commands:"
echo "  (gdb) continue          # Start/resume execution"
echo "  (gdb) break <function>  # Set breakpoint (e.g., 'break main.KernelMain')"
echo "  (gdb) info registers    # Show all registers"
echo "  (gdb) x/10i $pc         # Disassemble 10 instructions at PC"
echo "  (gdb) print/x $sp        # Print stack pointer in hex"
echo "  (gdb) print/x $x28       # Print g pointer (x28) in hex"
echo "  (gdb) info source       # Show current source file"
echo "  (gdb) list              # Show source code around current line"
echo "  (gdb) directory          # Show source search directories"
echo ""

# Start GDB and connect
exec "$GDB" "$KERNEL_ELF" \
    -ex "target remote localhost:$GDB_PORT" \
    -ex "set architecture aarch64" \
    -ex "directory $SRC_DIR" \
    -ex "directory $SRC_DIR/asm/aarch64" \
    -ex "directory $SRC_DIR/go/mazboot" \
    -ex "directory $SRC_DIR/bitfield" \
    -ex "set substitute-path /Users/iansmith/mazzy/src $SRC_DIR" \
    -ex "set substitute-path /Users/iansmith/mazzy/src/mazboot $SRC_DIR" \
    -ex "layout asm" \
    -ex "layout regs" \
    -ex "set disassembly-flavor intel"















