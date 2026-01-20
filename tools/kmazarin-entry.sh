#!/bin/bash
# kmazarin-entry.sh - Get kmazarin load address from Go constants
#
# PURPOSE:
#   This script retrieves the kmazarin load address from the cardinal/constants
#   package, maintaining a SINGLE SOURCE OF TRUTH for memory layout configuration.
#
# HOW IT WORKS:
#   Runs a simple Go program that imports cardinal/constants and prints
#   constants.KmazarinLoadAddr (computed from memory layout constants).
#
#   This replaces the old approach of parsing linker.ld, which is no longer
#   used since we switched to pure Go builds.
#
# USAGE:
#   ./kmazarin-entry.sh
#
# OUTPUT:
#   Prints the hex address (e.g., "0x41800000") to stdout
#
# USED BY:
#   Makefile: $(GO) build -ldflags="-T $(KMAZARIN_LOAD_ADDR)"

set -e

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Root of the project (one level up from tools/)
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Run the Go program to print the kmazarin load address
# We need to set the working directory to src/cardinal/golang so the import works
cd "$PROJECT_ROOT/src/cardinal/golang"
"$PROJECT_ROOT"/bin/go run "$SCRIPT_DIR/print-kmazarin-addr.go"
