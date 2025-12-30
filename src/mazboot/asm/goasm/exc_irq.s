// exc_irq.s - IRQ exception handlers (Go/Plan9 assembly)
//
// This file will contain:
// - irq_exception_el1: Main IRQ handler entry point
// - timer_preempt_handler: Timer interrupt for goroutine preemption
// - fiq_exception_el1: FIQ handler
// - serror_exception_el1: SError handler
//
// Migrated from: asm/aarch64/exceptions.s lines ~1540-1665

#include "textflag.h"

// TODO: Migrate IRQ handlers from exceptions.s
// Currently kept in GCC assembly
