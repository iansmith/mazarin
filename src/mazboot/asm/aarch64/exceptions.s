// exceptions.s - MOSTLY MIGRATED TO Go/Plan9 ASSEMBLY
//
// This file previously contained exception handlers and vector table.
// All exception handling code has been migrated to Go/Plan9 assembly:
//
//   exc_vectors.s     - Exception vector table (16 entries)
//   exc_handlers.s    - EL1t mode handlers (sync_exception_handler_el0, irq_exception_handler_el0)
//   exc_sync_entry.s  - Synchronous exception entry (sync_exception_handler)
//   exc_syscall.s     - Syscall dispatcher, exception return (sync_exception_entry, syscall_return, etc.)
//   exc_irq.s         - IRQ exception handler (irq_exception_el1)
//   exc_utils.s       - Debug print functions (print_hex64, print_string, etc.)
//
// The only remaining purpose of this file is to include kmazarin symbols
// for the remaining GCC assembly files (boot.s, writebarrier.s).

// Include kmazarin symbol addresses (auto-generated from kmazarin.elf)
// This is needed by any GCC assembly that references kmazarin symbols.
.include "build/mazboot/kmazarin_symbols.s"

// Switch to text section (required even if this file has no code)
.text
