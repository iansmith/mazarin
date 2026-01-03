#!/bin/bash

# Script to connect GDB to QEMU running in debug mode
# Usage: ./gdb-connect.sh [gdb-port]
#
# First, start QEMU with GDB server:
#   cd /Users/iansmith/mazzy && GDB=1 cardinal
#
# Then in another terminal, run this script:
#   cd /Users/iansmith/mazzy/tools/gdb && ./gdb-connect.sh

GDB_PORT="${1:-1234}"

# Use the cardinal.elf from build directory
KERNEL_ELF="../../build/cardinal.elf"

if [ ! -f "$KERNEL_ELF" ]; then
    echo "Error: cardinal.elf not found at $KERNEL_ELF" >&2
    echo "Please build cardinal first: make cardinal" >&2
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

# Get the absolute path to the source directory (src/cardinal)
SRC_DIR="$(cd "$(dirname "$0")/../../src/cardinal" && pwd)"
echo "Source directory: $SRC_DIR"
echo ""

echo "Useful GDB commands:"
echo "  (gdb) continue          # Start/resume execution"
echo "  (gdb) break <function>  # Set breakpoint (e.g., 'break main.KernelMain')"
echo "  (gdb) info registers    # Show all registers"
echo "  (gdb) x/10i \$pc         # Disassemble 10 instructions at PC"
echo "  (gdb) print/x \$sp       # Print stack pointer in hex"
echo "  (gdb) print/x \$x28      # Print g pointer (x28) in hex"
echo "  (gdb) info source       # Show current source file"
echo "  (gdb) list              # Show source code around current line"
echo "  (gdb) directory         # Show source search directories"
echo ""

# Start GDB and connect
exec "$GDB" "$KERNEL_ELF" \
    -ex "target remote localhost:$GDB_PORT" \
    -ex "set architecture aarch64" \
    -ex "directory $SRC_DIR" \
    -ex "directory $SRC_DIR/golang/main" \
    -ex "directory $SRC_DIR/golang/asm" \
    -ex "directory $SRC_DIR/golang/asm/arch/arm64" \
    -ex "directory $SRC_DIR/golang/asm/dev" \
    -ex "directory $SRC_DIR/golang/asm/kernel"















