#include "textflag.h"

// linker_symbols.s - Assembly helpers to access memory layout values
//
// These functions return the values stored in Go variables (defined in main/layout.go).
// The Go variables are initialized to 0 and may have their values injected at build time.
//
// IMPORTANT: These are ABI0 functions called from Go code.
// The return value MUST be stored to ret+0(FP), not just left in R0.
// The functions cannot use NOFRAME because they need to access FP.

// get_start_addr() returns uintptr
TEXT ·get_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_text_start_addr() returns uintptr
TEXT ·get_text_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerTextStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_text_end_addr() returns uintptr
TEXT ·get_text_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerTextEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_rodata_start_addr() returns uintptr
TEXT ·get_rodata_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerRodataStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_rodata_end_addr() returns uintptr
TEXT ·get_rodata_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerRodataEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_data_start_addr() returns uintptr
TEXT ·get_data_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerDataStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_data_end_addr() returns uintptr
TEXT ·get_data_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerDataEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_bss_start_addr() returns uintptr
TEXT ·get_bss_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerBssStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_bss_end_addr() returns uintptr
TEXT ·get_bss_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerBssEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_end_addr() returns uintptr
TEXT ·get_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_stack_top_addr() returns uintptr
TEXT ·get_stack_top_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerStackTop(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_page_tables_start_addr() returns uintptr
TEXT ·get_page_tables_start_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerPageTablesStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_page_tables_end_addr() returns uintptr
TEXT ·get_page_tables_end_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerPageTablesEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_ram_start() returns uintptr
TEXT ·get_ram_start(SB), NOSPLIT, $0-8
	MOVD	main·LinkerRamStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_dtb_boot_addr() returns uintptr
TEXT ·get_dtb_boot_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerDtbBootAddr(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_dtb_size() returns uintptr
TEXT ·get_dtb_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerDtbSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_cardinal_end() returns uintptr
TEXT ·get_cardinal_end(SB), NOSPLIT, $0-8
	MOVD	main·LinkerCardinalEnd(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_cardinal_allocation_size() returns uintptr
TEXT ·get_cardinal_allocation_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerCardinalAllocationSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_kmazarin_load_addr() returns uintptr
TEXT ·get_kmazarin_load_addr(SB), NOSPLIT, $0-8
	MOVD	main·LinkerKmazarinLoadAddr(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_g0_stack_bottom() returns uintptr
TEXT ·get_g0_stack_bottom(SB), NOSPLIT, $0-8
	MOVD	main·LinkerG0StackBottom(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// MMIO device addresses (QEMU virt machine)

// get_gic_base() returns uintptr
TEXT ·get_gic_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerGicBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_gic_size() returns uintptr
TEXT ·get_gic_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerGicSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_uart_base() returns uintptr
TEXT ·get_uart_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerUartBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_uart_size() returns uintptr
TEXT ·get_uart_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerUartSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_rtc_base() returns uintptr
TEXT ·get_rtc_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerRtcBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_fwcfg_base() returns uintptr
TEXT ·get_fwcfg_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerFwcfgBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_fwcfg_size() returns uintptr
TEXT ·get_fwcfg_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerFwcfgSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_bochs_display_base() returns uintptr
TEXT ·get_bochs_display_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerBochsDisplayBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_bochs_display_size() returns uintptr
TEXT ·get_bochs_display_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerBochsDisplaySize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_pci_bar_base() returns uintptr
TEXT ·get_pci_bar_base(SB), NOSPLIT, $0-8
	MOVD	main·LinkerPciBarBase(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_pci_bar_size() returns uintptr
TEXT ·get_pci_bar_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerPciBarSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// Embedded kmazarin kernel symbols

// get_kmazarin_start() returns uintptr
TEXT ·get_kmazarin_start(SB), NOSPLIT, $0-8
	MOVD	main·LinkerKmazarinStart(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_kmazarin_size() returns uintptr
TEXT ·get_kmazarin_size(SB), NOSPLIT, $0-8
	MOVD	main·LinkerKmazarinSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// Runtime Symbol Accessors
// ============================================================================
// These access runtime internal symbols and require -checklinkname=0

// get_runtime_mheap_addr() returns uintptr
TEXT ·get_runtime_mheap_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·mheap_(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_runtime_load_g_addr() returns uintptr
TEXT ·get_runtime_load_g_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·load_g(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_runtime_save_g_addr() returns uintptr
TEXT ·get_runtime_save_g_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·save_g(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_g0_addr() returns uintptr
TEXT ·get_g0_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·g0(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_m0_addr() returns uintptr
TEXT ·get_m0_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·m0(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_phys_page_size_addr() returns uintptr
TEXT ·get_phys_page_size_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·physPageSize(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_mcache0_addr() returns uintptr
TEXT ·get_mcache0_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·mcache0(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// get_emptymspan_addr() returns uintptr
TEXT ·get_emptymspan_addr(SB), NOSPLIT, $0-8
	MOVD	$runtime·emptymspan(SB), R0
	MOVD	R0, ret+0(FP)
	RET

