// exc_sync.s - Synchronous exception handlers (Go/Plan9 assembly)
//
// This file will contain:
// - sync_exception_el1: Entry point for synchronous exceptions
// - sync_exception_handler: Main synchronous exception dispatch
// - sync_unknown_exception: Handler for unknown exceptions
// - sync_other_exception: Handler for non-SVC exceptions
// - sync_restore_and_svc: SVC (syscall) handling entry
//
// Migrated from: asm/aarch64/exceptions.s lines ~1665-2000

#include "textflag.h"

// TODO: Migrate synchronous exception handlers from exceptions.s
// Currently kept in GCC assembly
