// exc_data.s - Exception handling data section (Go/Plan9 assembly)
//
// This file will contain:
// - Thread table and related globals
// - Timer tick counters
// - mmap bump allocator state
//
// NOTE: Data sections in Go/Plan9 assembly use DATA and GLOBL directives.
// Large uninitialized data (.space) may need special handling.
//
// Migrated from: asm/aarch64/exceptions.s lines 17-127

#include "textflag.h"

// TODO: Migrate data section from exceptions.s
// Currently kept in GCC assembly due to .space directive support needed
