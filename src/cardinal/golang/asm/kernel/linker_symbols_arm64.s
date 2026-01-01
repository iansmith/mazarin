#include "textflag.h"

// linker_symbols.s - Assembly helpers to access memory layout values
//
// These functions return the values stored in Go variables (defined in main/layout.go).
// The Go variables are initialized to 0 and may have their values injected at build time.
//
// IMPORTANT: Go's internal ABI passes return values in REGISTERS (R0).
// These functions load values from Go variables and return them in R0.

// get_start_addr() returns uintptr in R0
TEXT ·get_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerStart(SB), R0
	RET

// get_text_start_addr() returns uintptr in R0
TEXT ·get_text_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerTextStart(SB), R0
	RET

// get_text_end_addr() returns uintptr in R0
TEXT ·get_text_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerTextEnd(SB), R0
	RET

// get_rodata_start_addr() returns uintptr in R0
TEXT ·get_rodata_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerRodataStart(SB), R0
	RET

// get_rodata_end_addr() returns uintptr in R0
TEXT ·get_rodata_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerRodataEnd(SB), R0
	RET

// get_data_start_addr() returns uintptr in R0
TEXT ·get_data_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerDataStart(SB), R0
	RET

// get_data_end_addr() returns uintptr in R0
TEXT ·get_data_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerDataEnd(SB), R0
	RET

// get_bss_start_addr() returns uintptr in R0
TEXT ·get_bss_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerBssStart(SB), R0
	RET

// get_bss_end_addr() returns uintptr in R0
TEXT ·get_bss_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerBssEnd(SB), R0
	RET

// get_end_addr() returns uintptr in R0
TEXT ·get_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerEnd(SB), R0
	RET

// get_stack_top_addr() returns uintptr in R0
TEXT ·get_stack_top_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerStackTop(SB), R0
	RET

// get_page_tables_start_addr() returns uintptr in R0
TEXT ·get_page_tables_start_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerPageTablesStart(SB), R0
	RET

// get_page_tables_end_addr() returns uintptr in R0
TEXT ·get_page_tables_end_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerPageTablesEnd(SB), R0
	RET

// get_ram_start() returns uintptr in R0
TEXT ·get_ram_start(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerRamStart(SB), R0
	RET

// get_dtb_boot_addr() returns uintptr in R0
TEXT ·get_dtb_boot_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerDtbBootAddr(SB), R0
	RET

// get_dtb_size() returns uintptr in R0
TEXT ·get_dtb_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerDtbSize(SB), R0
	RET

// get_cardinal_end() returns uintptr in R0
TEXT ·get_cardinal_end(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerCardinalEnd(SB), R0
	RET

// get_cardinal_allocation_size() returns uintptr in R0
TEXT ·get_cardinal_allocation_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerCardinalAllocationSize(SB), R0
	RET

// get_kmazarin_load_addr() returns uintptr in R0
TEXT ·get_kmazarin_load_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerKmazarinLoadAddr(SB), R0
	RET

// get_g0_stack_bottom() returns uintptr in R0
TEXT ·get_g0_stack_bottom(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerG0StackBottom(SB), R0
	RET

// MMIO device addresses (QEMU virt machine)

// get_gic_base() returns uintptr in R0
TEXT ·get_gic_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerGicBase(SB), R0
	RET

// get_gic_size() returns uintptr in R0
TEXT ·get_gic_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerGicSize(SB), R0
	RET

// get_uart_base() returns uintptr in R0
TEXT ·get_uart_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerUartBase(SB), R0
	RET

// get_uart_size() returns uintptr in R0
TEXT ·get_uart_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerUartSize(SB), R0
	RET

// get_rtc_base() returns uintptr in R0
TEXT ·get_rtc_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerRtcBase(SB), R0
	RET

// get_fwcfg_base() returns uintptr in R0
TEXT ·get_fwcfg_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerFwcfgBase(SB), R0
	RET

// get_fwcfg_size() returns uintptr in R0
TEXT ·get_fwcfg_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerFwcfgSize(SB), R0
	RET

// get_bochs_display_base() returns uintptr in R0
TEXT ·get_bochs_display_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerBochsDisplayBase(SB), R0
	RET

// get_bochs_display_size() returns uintptr in R0
TEXT ·get_bochs_display_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerBochsDisplaySize(SB), R0
	RET

// get_pci_bar_base() returns uintptr in R0
TEXT ·get_pci_bar_base(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerPciBarBase(SB), R0
	RET

// get_pci_bar_size() returns uintptr in R0
TEXT ·get_pci_bar_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerPciBarSize(SB), R0
	RET

// Embedded kmazarin kernel symbols

// get_kmazarin_start() returns uintptr in R0
TEXT ·get_kmazarin_start(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerKmazarinStart(SB), R0
	RET

// get_kmazarin_size() returns uintptr in R0
TEXT ·get_kmazarin_size(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	main·LinkerKmazarinSize(SB), R0
	RET

// ============================================================================
// Runtime Symbol Accessors
// ============================================================================
// These access runtime internal symbols and require -checklinkname=0

// get_runtime_mheap_addr() returns uintptr in R0
TEXT ·get_runtime_mheap_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·mheap_(SB), R0
	RET

// get_runtime_load_g_addr() returns uintptr in R0
TEXT ·get_runtime_load_g_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·load_g(SB), R0
	RET

// get_runtime_save_g_addr() returns uintptr in R0
TEXT ·get_runtime_save_g_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·save_g(SB), R0
	RET

// get_g0_addr() returns uintptr in R0
TEXT ·get_g0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·g0(SB), R0
	RET

// get_m0_addr() returns uintptr in R0
TEXT ·get_m0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·m0(SB), R0
	RET

// get_phys_page_size_addr() returns uintptr in R0
TEXT ·get_phys_page_size_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·physPageSize(SB), R0
	RET

// get_mcache0_addr() returns uintptr in R0
TEXT ·get_mcache0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·mcache0(SB), R0
	RET

// get_emptymspan_addr() returns uintptr in R0
TEXT ·get_emptymspan_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$runtime·emptymspan(SB), R0
	RET
