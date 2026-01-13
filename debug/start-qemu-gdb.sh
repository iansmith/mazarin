#!/bin/bash
# Helper script to start QEMU with GDB server enabled
# After running this, connect with GDB from another terminal

cd /Users/iansmith/mazzy

echo "Starting QEMU with GDB server on port 1234..."
echo ""
echo "QEMU will pause at startup waiting for GDB to connect."
echo ""
echo "In another terminal, run one of:"
echo "  gdb-multiarch -x debug/gdb-adrp-debug.gdb build/kmazarin.elf"
echo "  gdb-multiarch -x debug/gdb-trace-entry.gdb build/cardinal.elf"
echo "  gdb-multiarch -x debug/gdb-check-mappings.gdb"
echo ""
echo "Or connect manually:"
echo "  gdb-multiarch build/kmazarin.elf"
echo "  (gdb) target remote localhost:1234"
echo ""
echo "Starting QEMU..."
echo ""

exec ~/mazzy/bin/qemu-system-aarch64 \
    -S \
    -s \
    -M virt,virtualization=off \
    -cpu cortex-a72 \
    -m 8G \
    -kernel build/cardinal.elf \
    -nodefaults \
    -device bochs-display \
    -display none \
    -serial stdio \
    -no-reboot
