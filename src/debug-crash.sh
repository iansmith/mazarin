#!/bin/bash
# Script to analyze crash addresses from QEMU output
# Usage: ./debug-crash.sh <ELR_address> [FAR_address]
# Example: ./debug-crash.sh 0x2bf760 0xffffffffffffffff

if [ $# -lt 1 ]; then
    echo "Usage: $0 <ELR_address> [FAR_address]"
    echo "Example: $0 0x2bf760 0xffffffffffffffff"
    exit 1
fi

ELR=$1
FAR=${2:-"N/A"}

source /Users/iansmith/mazzy/enable-mazzy

echo "=== Crash Analysis ==="
echo "ELR (Exception Link Register): $ELR"
echo "FAR (Fault Address Register): $FAR"
echo ""

echo "=== Function and Source Location ==="
target-addr2line -e kernel-qemu.elf -f -C -p $ELR
echo ""

echo "=== Disassembly Around Crash ==="
target-objdump -d kernel-qemu.elf | grep -A 10 -B 5 "$(echo $ELR | sed 's/0x//')"
echo ""

echo "=== Symbol Information ==="
# Find which function contains this address
target-nm kernel-qemu.elf | awk -v addr=$(echo $ELR | sed 's/0x//') '
BEGIN { prev_addr = 0; prev_name = "" }
{
    if ($1 ~ /^[0-9a-f]+$/) {
        current_addr = strtonum("0x" $1)
        if (current_addr <= strtonum("0x" addr) && current_addr > prev_addr) {
            prev_addr = current_addr
            prev_name = $3
        }
    }
}
END { if (prev_name != "") print "Function: " prev_name " (starts at 0x" sprintf("%x", prev_addr) ")" }'
echo ""

echo "=== Source Code Context (if available) ==="
target-objdump -S kernel-qemu.elf 2>/dev/null | grep -A 15 -B 5 "$(echo $ELR | sed 's/0x//')" | head -25 || echo "Source code not available in this build"
echo ""

echo "=== Nearby Symbols ==="
target-nm kernel-qemu.elf | grep -E "^[0-9a-f]+" | awk -v addr=$(echo $ELR | sed 's/0x//') '
{
    if ($1 ~ /^[0-9a-f]+$/) {
        sym_addr = strtonum("0x" $1)
        diff = sym_addr - strtonum("0x" addr)
        if (diff >= -0x100 && diff <= 0x100) {
            printf "0x%x %s (offset: %+d)\n", sym_addr, $3, diff
        }
    }
}' | sort -k2
