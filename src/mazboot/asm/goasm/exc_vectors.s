// exc_vectors.s - Exception vector table (Go/Plan9 assembly)
//
// This file will contain:
// - exception_vectors: The 2KB-aligned exception vector table
// - set_vbar_el1: Set VBAR_EL1 to point to vectors
// - read_vbar_el1: Read current VBAR_EL1
// - get_exception_vectors_addr: Get address of vector table
// - enable_irqs / disable_irqs: IRQ control
// - read_spsr_el1 / write_spsr_el1: SPSR access
//
// NOTE: The exception vector table MUST be 2KB (2048 byte) aligned.
// Go/Plan9 assembly may need special handling for this alignment.
//
// Migrated from: asm/aarch64/exceptions.s lines ~1200-1540

#include "textflag.h"

// TODO: Migrate exception vector table from exceptions.s
// Currently kept in GCC assembly due to alignment requirements
