#include "textflag.h"

// linker_symbols.s - Assembly helpers to access linker-defined symbols
// These functions return the actual addresses of linker symbols, avoiding hardcoded values
// Converted to Go Plan 9 assembly syntax
//
// IMPORTANT: Go's internal ABI passes arguments in REGISTERS (R0, R1, etc.), not on the stack.
// Return values are expected in R0, not stored to ret+0(FP).

// get_start_addr() returns uintptr in R0
TEXT ·get_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__start(SB), R0
	RET

// get_text_start_addr() returns uintptr in R0
TEXT ·get_text_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__text_start(SB), R0
	RET

// get_text_end_addr() returns uintptr in R0
TEXT ·get_text_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__text_end(SB), R0
	RET

// get_rodata_start_addr() returns uintptr in R0
TEXT ·get_rodata_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__rodata_start(SB), R0
	RET

// get_rodata_end_addr() returns uintptr in R0
TEXT ·get_rodata_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__rodata_end(SB), R0
	RET

// get_data_start_addr() returns uintptr in R0
TEXT ·get_data_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__data_start(SB), R0
	RET

// get_data_end_addr() returns uintptr in R0
TEXT ·get_data_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__data_end(SB), R0
	RET

// get_bss_start_addr() returns uintptr in R0
TEXT ·get_bss_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__bss_start(SB), R0
	RET

// get_bss_end_addr() returns uintptr in R0
TEXT ·get_bss_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__bss_end(SB), R0
	RET

// get_end_addr() returns uintptr in R0
TEXT ·get_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__end(SB), R0
	RET

// get_stack_top_addr() returns uintptr in R0
TEXT ·get_stack_top_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__stack_top(SB), R0
	RET

// get_page_tables_start_addr() returns uintptr in R0
TEXT ·get_page_tables_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__page_tables_start(SB), R0
	RET

// get_page_tables_end_addr() returns uintptr in R0
TEXT ·get_page_tables_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__page_tables_end(SB), R0
	RET

// get_ram_start() returns uintptr in R0
// Returns QEMU's physical RAM base address (platform-specific)
TEXT ·get_ram_start(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__ram_start(SB), R0
	RET

// get_dtb_boot_addr() returns uintptr in R0
// Returns QEMU's DTB location (platform-specific, not part of relocatable layout)
TEXT ·get_dtb_boot_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__dtb_boot_addr(SB), R0
	RET

// get_dtb_size() returns uintptr in R0
// Returns DTB size (1MB reserved by QEMU)
TEXT ·get_dtb_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__dtb_size(SB), R0
	RET

// get_cardinal_end() returns uintptr in R0
// Returns end of cardinal 15MB allocation (where kmazarin starts)
TEXT ·get_cardinal_end(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__cardinal_end(SB), R0
	RET

// get_cardinal_allocation_size() returns uintptr in R0
// Returns total cardinal allocation size (15MB)
TEXT ·get_cardinal_allocation_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__cardinal_allocation_size(SB), R0
	RET

// get_kmazarin_load_addr() returns uintptr in R0
// Returns virtual address where kmazarin should be loaded
TEXT ·get_kmazarin_load_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__kmazarin_load_addr(SB), R0
	RET

// get_g0_stack_bottom() returns uintptr in R0
// Returns bottom of g0 stack (32KB below stack top)
TEXT ·get_g0_stack_bottom(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__g0_stack_bottom(SB), R0
	RET

// MMIO device addresses (QEMU virt machine)

// get_gic_base() returns uintptr in R0
TEXT ·get_gic_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__gic_base(SB), R0
	RET

// get_gic_size() returns uintptr in R0
TEXT ·get_gic_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__gic_size(SB), R0
	RET

// get_uart_base() returns uintptr in R0
TEXT ·get_uart_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__uart_base(SB), R0
	RET

// get_uart_size() returns uintptr in R0
TEXT ·get_uart_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__uart_size(SB), R0
	RET

// get_rtc_base() returns uintptr in R0
TEXT ·get_rtc_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__rtc_base(SB), R0
	RET

// get_fwcfg_base() returns uintptr in R0
TEXT ·get_fwcfg_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__fwcfg_base(SB), R0
	RET

// get_fwcfg_size() returns uintptr in R0
TEXT ·get_fwcfg_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__fwcfg_size(SB), R0
	RET

// get_bochs_display_base() returns uintptr in R0
TEXT ·get_bochs_display_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__bochs_display_base(SB), R0
	RET

// get_bochs_display_size() returns uintptr in R0
TEXT ·get_bochs_display_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__bochs_display_size(SB), R0
	RET

// get_pci_bar_base() returns uintptr in R0
TEXT ·get_pci_bar_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__pci_bar_base(SB), R0
	RET

// get_pci_bar_size() returns uintptr in R0
TEXT ·get_pci_bar_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__pci_bar_size(SB), R0
	RET

// Embedded kmazarin kernel symbols

// get_kmazarin_start() returns uintptr in R0
TEXT ·get_kmazarin_start(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__kmazarin_start(SB), R0
	RET

// get_kmazarin_size() returns uintptr in R0
TEXT ·get_kmazarin_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$__kmazarin_size(SB), R0
	RET

// get_runtime_mheap_addr() returns uintptr in R0
// Returns the address of runtime.mheap_ global variable
TEXT ·get_runtime_mheap_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·mheap_(SB), R0
	RET

// get_runtime_load_g_addr() returns uintptr in R0
TEXT ·get_runtime_load_g_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·load_g·abi0(SB), R0
	RET

// get_runtime_save_g_addr() returns uintptr in R0
TEXT ·get_runtime_save_g_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·save_g·abi0(SB), R0
	RET

// get_g0_addr() returns uintptr in R0
// Returns the address of runtime.g0
TEXT ·get_g0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·g0(SB), R0
	RET

// get_m0_addr() returns uintptr in R0
// Returns the address of runtime.m0
TEXT ·get_m0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·m0(SB), R0
	RET

// get_phys_page_size_addr() returns uintptr in R0
// Returns the address of runtime.physPageSize
TEXT ·get_phys_page_size_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·physPageSize(SB), R0
	RET

// get_mcache0_addr() returns uintptr in R0
// Returns the address of runtime.mcache0
TEXT ·get_mcache0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·mcache0(SB), R0
	RET

// get_emptymspan_addr() returns uintptr in R0
// Returns the address of runtime.emptymspan
TEXT ·get_emptymspan_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·emptymspan(SB), R0
	RET
