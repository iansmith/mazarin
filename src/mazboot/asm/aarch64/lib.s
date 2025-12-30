// lib.s - DEPRECATED: All functions migrated to Go/Plan9 assembly
//
// This file is now empty. All functions have been migrated to:
//   - lib_runtime.s (Go/Plan9): runtime wrappers, kernel_main, morestack, jump_to_kmazarin
//   - lib_misc.s (Go/Plan9): UART functions (uart_init_pl011, uart_putc_pl011)
//   - lib_barriers.s (Go/Plan9): barrier functions, Nop
//   - lib_getters.s (Go/Plan9): simple getter functions
//   - lib_mmio.s (Go/Plan9): MMIO functions
//   - lib_sysregs.s (Go/Plan9): system register functions
//   - lib_timer.s (Go/Plan9): timer functions
//   - lib_mmu.s (Go/Plan9): MMU functions
//
// The Go/Plan9 assembly files are located in asm/goasm/ and are transpiled
// to ELF objects using the goasm2gnu tool.

.section ".text"
// (intentionally empty - all code migrated)
