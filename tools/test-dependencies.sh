#!/bin/bash
# test-dependencies.sh - Prove that the build dependency chain works correctly
#
# This script tests that changes to any input file trigger proper rebuilds:
# 1. Changing constants/layout.go → cardinal rebuilds
# 2. Changing kmazarin source → kmazarin rebuilds → data rebuilds → cardinal rebuilds
# 3. Changing compute-linker-values.go → cardinal rebuilds and re-patches
#
# Usage: ./tools/test-dependencies.sh

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "========================================="
echo "Testing Build Dependency Chain"
echo "========================================="

# Helper to print test results
pass() {
    echo -e "${GREEN}✅ PASS:${NC} $1"
}

fail() {
    echo -e "${RED}❌ FAIL:${NC} $1"
    exit 1
}

info() {
    echo -e "${YELLOW}ℹ${NC}  $1"
}

# Clean and initial build
info "Step 1: Clean build"
make clean > /dev/null 2>&1
make cardinal > /dev/null 2>&1 || fail "Initial build failed"
pass "Initial build successful"

# Get initial timestamps
CARDINAL_TIME=$(stat -f %m build/cardinal.elf 2>/dev/null || stat -c %Y build/cardinal.elf)
KMAZARIN_TIME=$(stat -f %m build/kmazarin/kmazarin.elf 2>/dev/null || stat -c %Y build/kmazarin/kmazarin.elf)
DATA_TIME=$(stat -f %m src/cardinal/golang/asm/dev/kmazarin_data_arm64.s 2>/dev/null || stat -c %Y src/cardinal/golang/asm/dev/kmazarin_data_arm64.s)

info "Initial timestamps:"
info "  cardinal.elf: $CARDINAL_TIME"
info "  kmazarin.elf: $KMAZARIN_TIME"
info "  kmazarin_data_arm64.s: $DATA_TIME"

# Test 1: Touch constants/layout.go → cardinal should rebuild
echo ""
info "Test 1: Changing constants/layout.go should rebuild cardinal"
sleep 1  # Ensure timestamp difference
touch src/cardinal/golang/constants/layout.go
make cardinal > /dev/null 2>&1

NEW_CARDINAL_TIME=$(stat -f %m build/cardinal.elf 2>/dev/null || stat -c %Y build/cardinal.elf)
if [ "$NEW_CARDINAL_TIME" -gt "$CARDINAL_TIME" ]; then
    pass "Cardinal rebuilt after constants change"
else
    fail "Cardinal did NOT rebuild after constants change"
fi

# Test 2: Touch kmazarin source → kmazarin, data, cardinal should all rebuild
echo ""
info "Test 2: Changing kmazarin source should rebuild kmazarin, data, cardinal"
sleep 1
touch src/kmazarin/golang/kmazarin/main.go
make cardinal > /dev/null 2>&1

NEW_KMAZARIN_TIME=$(stat -f %m build/kmazarin/kmazarin.elf 2>/dev/null || stat -c %Y build/kmazarin/kmazarin.elf)
NEW_DATA_TIME=$(stat -f %m src/cardinal/golang/asm/dev/kmazarin_data_arm64.s 2>/dev/null || stat -c %Y src/cardinal/golang/asm/dev/kmazarin_data_arm64.s)
NEW_CARDINAL_TIME2=$(stat -f %m build/cardinal.elf 2>/dev/null || stat -c %Y build/cardinal.elf)

if [ "$NEW_KMAZARIN_TIME" -gt "$KMAZARIN_TIME" ]; then
    pass "Kmazarin rebuilt after source change"
else
    fail "Kmazarin did NOT rebuild after source change"
fi

if [ "$NEW_DATA_TIME" -gt "$DATA_TIME" ]; then
    pass "Kmazarin data regenerated after kmazarin change"
else
    fail "Kmazarin data did NOT regenerate"
fi

if [ "$NEW_CARDINAL_TIME2" -gt "$NEW_CARDINAL_TIME" ]; then
    pass "Cardinal rebuilt after kmazarin/data change"
else
    fail "Cardinal did NOT rebuild after kmazarin change"
fi

# Test 3: Touch compute-linker-values.go → cardinal should rebuild
echo ""
info "Test 3: Changing compute-linker-values.go should rebuild cardinal"
sleep 1
touch src/cardinal/tools/compute-linker-values.go
make cardinal > /dev/null 2>&1

NEW_CARDINAL_TIME3=$(stat -f %m build/cardinal.elf 2>/dev/null || stat -c %Y build/cardinal.elf)
if [ "$NEW_CARDINAL_TIME3" -gt "$NEW_CARDINAL_TIME2" ]; then
    pass "Cardinal rebuilt after patching tool change"
else
    fail "Cardinal did NOT rebuild after patching tool change"
fi

echo ""
echo "========================================="
echo -e "${GREEN}✅ All dependency tests PASSED!${NC}"
echo "========================================="
echo ""
echo "Verified dependency chain:"
echo "  1. constants/layout.go → cardinal"
echo "  2. kmazarin source → kmazarin → data → cardinal"
echo "  3. compute-linker-values.go → cardinal"
