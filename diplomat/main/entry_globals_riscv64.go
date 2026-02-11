//go:build riscv64

package main

// Globals set by assembly entry point from OpenSBI parameters
var savedHartID uint64   // Hart ID from A0 register at entry
var savedFDTAddr uint64  // FDT physical address from A1 register at entry

// Physical addresses of key regions set up by assembly
var ptPoolBase uint64      // Base of page table pool
var bumpAllocStart uint64  // Start of bump allocator region
var g0StackPA uint64       // Physical address of g0 stack base
var excStackPA uint64      // Physical address of exception stack base
