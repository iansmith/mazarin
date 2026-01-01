// layout.go - Memory layout variables for Cardinal on QEMU virt machine
//
// These variables replace the GNU linker script symbols. They are initialized
// to 0 and may have their values injected by the build process (e.g., via
// post-processing or -X linker flag).
//
// Variable naming: All symbols are prefixed with "Linker" to indicate they
// correspond to linker script symbols and may be injected.

package main

// ============================================================================
// Linker Symbol Variables
// ============================================================================
// These variables correspond to the symbols defined in linker.ld.
// They are initialized to 0 and values may be injected at build time.

var (
	// Section boundaries
	LinkerStart       uint64 = 0 // __start
	LinkerTextStart   uint64 = 0 // __text_start
	LinkerTextEnd     uint64 = 0 // __text_end
	LinkerRodataStart uint64 = 0 // __rodata_start
	LinkerRodataEnd   uint64 = 0 // __rodata_end
	LinkerDataStart   uint64 = 0 // __data_start
	LinkerDataEnd     uint64 = 0 // __data_end
	LinkerBssStart    uint64 = 0 // __bss_start
	LinkerBssEnd      uint64 = 0 // __bss_end
	LinkerEnd         uint64 = 0 // __end

	// Memory layout boundaries
	LinkerRamStart              uint64 = 0 // __ram_start
	LinkerDtbBootAddr           uint64 = 0 // __dtb_boot_addr
	LinkerDtbSize               uint64 = 0 // __dtb_size
	LinkerCardinalEnd           uint64 = 0 // __cardinal_end
	LinkerCardinalAllocationSize uint64 = 0 // __cardinal_allocation_size
	LinkerKmazarinLoadAddr      uint64 = 0 // __kmazarin_load_addr
	LinkerPageTablesStart       uint64 = 0 // __page_tables_start
	LinkerPageTablesEnd         uint64 = 0 // __page_tables_end

	// Stack pointers
	LinkerStackTop      uint64 = 0 // __stack_top
	LinkerG0StackBottom uint64 = 0 // __g0_stack_bottom

	// MMIO device addresses
	LinkerGicBase          uint64 = 0 // __gic_base
	LinkerGicSize          uint64 = 0 // __gic_size
	LinkerUartBase         uint64 = 0 // __uart_base
	LinkerUartSize         uint64 = 0 // __uart_size
	LinkerRtcBase          uint64 = 0 // __rtc_base
	LinkerFwcfgBase        uint64 = 0 // __fwcfg_base
	LinkerFwcfgSize        uint64 = 0 // __fwcfg_size
	LinkerBochsDisplayBase uint64 = 0 // __bochs_display_base
	LinkerBochsDisplaySize uint64 = 0 // __bochs_display_size
	LinkerPciBarBase       uint64 = 0 // __pci_bar_base
	LinkerPciBarSize       uint64 = 0 // __pci_bar_size

	// Embedded kmazarin kernel
	LinkerKmazarinStart uint64 = 0 // __kmazarin_start
	LinkerKmazarinSize  uint64 = 0 // __kmazarin_size
)
