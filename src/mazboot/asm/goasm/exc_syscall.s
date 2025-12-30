// exc_syscall.s - Syscall dispatch and handlers (Go/Plan9 assembly)
//
// This file will contain:
// - handle_svc_syscall: Main syscall dispatch
// - syscall_write / syscall_read / syscall_openat / syscall_close
// - syscall_futex / syscall_nanosleep
// - syscall_mmap / syscall_munmap / syscall_brk
// - syscall_clone_fake / syscall_exit
// - syscall_rt_sigaction / syscall_rt_sigprocmask
// - syscall_clock_gettime / syscall_getrandom
// - And many more syscall handlers...
// - syscall_return: Common return path
// - load_context_and_eret: Context restore and return from exception
//
// Migrated from: asm/aarch64/exceptions.s lines ~2000-2650

#include "textflag.h"

// TODO: Migrate syscall handlers from exceptions.s
// Currently kept in GCC assembly
