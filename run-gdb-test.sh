#!/bin/bash
# Test GDB connection and run our debug script

cd /Users/iansmith/mazzy

# Run GDB with our debug script
~/mazzy/bin/target-gdb flash/mazboot.elf \
    -ex 'source debug-simple.gdb' \
    -ex 'continue' \
    -ex 'quit'
