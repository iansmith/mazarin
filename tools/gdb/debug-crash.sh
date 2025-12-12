#!/bin/bash

# Script to debug the crash location
# This will start QEMU in GDB mode and connect GDB automatically

set -e

cd "$(dirname "$0")"
cd ../..

# Source environment
source enable-mazzy

echo "=== Starting QEMU with GDB server ==="
echo "This will start QEMU and wait for GDB to connect..."
echo ""

# Start QEMU in background with GDB
docker/mazboot -g > /tmp/qemu-output.log 2>&1 &
QEMU_PID=$!

echo "QEMU started with PID: $QEMU_PID"
echo "Waiting for QEMU to initialize..."
sleep 2

# Check if QEMU is still running
if ! kill -0 $QEMU_PID 2>/dev/null; then
    echo "Error: QEMU exited unexpectedly"
    cat /tmp/qemu-output.log
    exit 1
fi

echo ""
echo "=== Connecting GDB ==="
echo ""

# Connect GDB
cd "$(dirname "$0")"
./gdb-connect.sh

# Cleanup
echo ""
echo "=== Cleaning up ==="
kill $QEMU_PID 2>/dev/null || true
