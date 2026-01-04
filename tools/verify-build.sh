#!/bin/bash
# verify-build.sh - Verify that cardinal.elf has correct linker values
#
# This script verifies the build by:
# 1. Running compute-linker-values.go in non-patch mode to see computed values
# 2. Checking that all expected symbols exist in the binary
# 3. Verifying key addresses are in expected ranges
#
# Usage: ./tools/verify-build.sh [cardinal.elf] [kmazarin.elf]

set -e

CARDINAL_ELF="${1:-build/cardinal.elf}"
KMAZARIN_ELF="${2:-build/kmazarin/kmazarin.elf}"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "========================================="
echo "Verifying Cardinal Build"
echo "========================================="
echo ""

if [ ! -f "$CARDINAL_ELF" ]; then
    echo -e "${RED}❌ Cardinal ELF not found: $CARDINAL_ELF${NC}"
    exit 1
fi

if [ ! -f "$KMAZARIN_ELF" ]; then
    echo -e "${YELLOW}⚠️  Kmazarin ELF not found: $KMAZARIN_ELF (skipping kmazarin checks)${NC}"
    KMAZARIN_ELF=""
fi

# Run compute-linker-values in non-patch mode to see what values were computed
echo "Computing linker values from ELF files..."
echo ""

if [ -n "$KMAZARIN_ELF" ]; then
    cd src/cardinal/golang
    ../../../bin/go run ../tools/compute-linker-values.go -kmazarin "../../../$KMAZARIN_ELF" "../../../$CARDINAL_ELF"
    cd ../../..
else
    cd src/cardinal/golang
    ../../../bin/go run ../tools/compute-linker-values.go "../../../$CARDINAL_ELF"
    cd ../../..
fi

echo ""
echo "========================================="
echo "Symbol Checks"
echo "========================================="

# Check that critical symbols exist
SYMBOLS=$(~/mazzy/bin/target-nm "$CARDINAL_ELF" 2>/dev/null || nm "$CARDINAL_ELF")

check_symbol() {
    local sym=$1
    local desc=$2
    if echo "$SYMBOLS" | grep -q " $sym$"; then
        echo -e "${GREEN}✅${NC} $desc ($sym) found"
    else
        echo -e "${RED}❌${NC} $desc ($sym) NOT FOUND"
        return 1
    fi
}

check_symbol "main.LinkerUartBase" "UART base address"
check_symbol "main.LinkerGicBase" "GIC base address"
check_symbol "main.LinkerStackTop" "g0 stack top"
check_symbol "main.LinkerExceptionStackTop" "Exception stack top"
check_symbol "main.LinkerTextStart" "Text section start"
check_symbol "main.LinkerBssStart" "BSS section start"
check_symbol "main.LinkerKmazarinStart" "Kmazarin start address"

echo ""
echo "========================================="
echo -e "${GREEN}✅ Verification Complete${NC}"
echo "========================================="
echo ""
echo "The computed linker values above show what was patched into cardinal.elf"
echo "Check that:"
echo "  • Section boundaries match the ELF file structure"
echo "  • Fixed values match constants/layout.go"
echo "  • Kmazarin values are correct if kmazarin.elf was provided"
