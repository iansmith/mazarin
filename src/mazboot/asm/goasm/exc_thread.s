// exc_thread.s - Thread management functions (Go/Plan9 assembly)
//
// This file will contain:
// - thread_get_entry: Get pointer to thread entry by index
// - thread_save_context: Save current thread context
// - thread_save_context_from_frame: Save context from exception frame
// - thread_find_ready: Find next ready thread to run
// - thread_switch_to: Switch to a different thread
// - thread_create: Create a new thread
// - thread_wake_futex: Wake threads waiting on futex
// - thread_check_sleepers: Check and wake sleeping threads
//
// Migrated from: asm/aarch64/exceptions.s lines ~600-1200

#include "textflag.h"

// TODO: Migrate thread management functions from exceptions.s
// Currently kept in GCC assembly
