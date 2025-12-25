#!/bin/bash
# kmazarin-entry.sh - Extract kmazarin load address from linker script
#
# PURPOSE:
#   This script ensures the Makefile and linker.ld stay in sync by calculating
#   the kmazarin load address directly from the linker script constants.
#   This maintains a SINGLE SOURCE OF TRUTH for memory layout configuration.
#
# HOW IT WORKS:
#   Reads from linker.ld:
#     BOOT_ADDRESS = 0x40000000
#     DTB_SIZE = 0x100000
#     ALLOCATION_SIZE_MAZBOOT = 0xF00000
#     PAGE_TABLE_SIZE = 0x800000
#
#   Calculates:
#     KMAZARIN_START = BOOT_ADDRESS + DTB_SIZE + ALLOCATION_SIZE_MAZBOOT + PAGE_TABLE_SIZE
#                    = 0x40000000 + 0x100000 + 0xF00000 + 0x800000
#                    = 0x41800000
#
# USAGE:
#   ./kmazarin-entry.sh [path/to/linker.ld]
#
# OUTPUT:
#   Prints the hex address (e.g., "0x41000000") to stdout
#
# USED BY:
#   Makefile: $(GO) build -ldflags="-T $(KMAZARIN_LOAD_ADDR)"

set -e

# Default linker script path
LINKER_SCRIPT="${1:-src/mazboot/linker.ld}"

# Check if linker script exists
if [ ! -f "$LINKER_SCRIPT" ]; then
    echo "ERROR: Linker script not found: $LINKER_SCRIPT" >&2
    exit 1
fi

# Extract KMAZARIN_START calculation from linker script
# This reads the line that defines KMAZARIN_START and evaluates it
#
# Strategy:
# 1. Find all constant definitions (BOOT_ADDRESS, DTB_SIZE, ALLOCATION_SIZE_MAZBOOT)
# 2. Find KMAZARIN_START definition
# 3. Calculate the final address
#
# The linker script defines:
#   BOOT_ADDRESS = 0x40000000;
#   DTB_SIZE = 0x100000;
#   ALLOCATION_SIZE_MAZBOOT = 0xF00000;
#   PAGE_TABLE_SIZE = 0x800000;
#   KMAZARIN_START = PAGE_TABLE_END;
#   PAGE_TABLE_END = PAGE_TABLE_START + PAGE_TABLE_SIZE;
#   PAGE_TABLE_START = MAZBOOT_END;
#   MAZBOOT_END = MAZBOOT_START + ALLOCATION_SIZE_MAZBOOT;
#   MAZBOOT_START = DTB_END;
#   DTB_END = DTB_START + DTB_SIZE;
#   DTB_START = BOOT_ADDRESS;
#
# So: KMAZARIN_START = BOOT_ADDRESS + DTB_SIZE + ALLOCATION_SIZE_MAZBOOT + PAGE_TABLE_SIZE

# Extract BOOT_ADDRESS (e.g., "BOOT_ADDRESS = 0x40000000;")
BOOT_ADDRESS=$(grep -E '^\s*BOOT_ADDRESS\s*=' "$LINKER_SCRIPT" | \
               grep -oE '0x[0-9A-Fa-f]+')

# Extract DTB_SIZE (e.g., "DTB_SIZE = 0x100000;")
DTB_SIZE=$(grep -E '^\s*DTB_SIZE\s*=' "$LINKER_SCRIPT" | \
           grep -oE '0x[0-9A-Fa-f]+')

# Extract ALLOCATION_SIZE_MAZBOOT (e.g., "ALLOCATION_SIZE_MAZBOOT = 0xF00000;")
ALLOCATION_SIZE_MAZBOOT=$(grep -E '^\s*ALLOCATION_SIZE_MAZBOOT\s*=' "$LINKER_SCRIPT" | \
                          grep -oE '0x[0-9A-Fa-f]+')

# Extract PAGE_TABLE_SIZE (e.g., "PAGE_TABLE_SIZE = 0x800000;")
PAGE_TABLE_SIZE=$(grep -E '^\s*PAGE_TABLE_SIZE\s*=' "$LINKER_SCRIPT" | \
                  grep -oE '0x[0-9A-Fa-f]+')

# Validate we found all values
if [ -z "$BOOT_ADDRESS" ] || [ -z "$DTB_SIZE" ] || [ -z "$ALLOCATION_SIZE_MAZBOOT" ] || [ -z "$PAGE_TABLE_SIZE" ]; then
    echo "ERROR: Failed to extract linker script constants" >&2
    echo "  BOOT_ADDRESS=$BOOT_ADDRESS" >&2
    echo "  DTB_SIZE=$DTB_SIZE" >&2
    echo "  ALLOCATION_SIZE_MAZBOOT=$ALLOCATION_SIZE_MAZBOOT" >&2
    echo "  PAGE_TABLE_SIZE=$PAGE_TABLE_SIZE" >&2
    exit 1
fi

# Calculate KMAZARIN_START = BOOT_ADDRESS + DTB_SIZE + ALLOCATION_SIZE_MAZBOOT + PAGE_TABLE_SIZE
# Use printf to do hex arithmetic
KMAZARIN_START=$(printf "0x%X" $(( $BOOT_ADDRESS + $DTB_SIZE + $ALLOCATION_SIZE_MAZBOOT + $PAGE_TABLE_SIZE )))

# Output just the address (for use in Makefile)
echo "$KMAZARIN_START"
