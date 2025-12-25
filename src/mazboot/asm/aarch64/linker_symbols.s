// linker_symbols.s - Assembly helpers to access linker-defined symbols
// These functions return the actual addresses of linker symbols, avoiding hardcoded values

.section .text

// get_start_addr() returns uintptr
.global get_start_addr
get_start_addr:
    ldr x0, =__start
    ret

// get_text_start_addr() returns uintptr
.global get_text_start_addr
get_text_start_addr:
    ldr x0, =__text_start
    ret

// get_text_end_addr() returns uintptr
.global get_text_end_addr
get_text_end_addr:
    ldr x0, =__text_end
    ret

// get_rodata_start_addr() returns uintptr
.global get_rodata_start_addr
get_rodata_start_addr:
    ldr x0, =__rodata_start
    ret

// get_rodata_end_addr() returns uintptr
.global get_rodata_end_addr
get_rodata_end_addr:
    ldr x0, =__rodata_end
    ret

// get_data_start_addr() returns uintptr
.global get_data_start_addr
get_data_start_addr:
    ldr x0, =__data_start
    ret

// get_data_end_addr() returns uintptr
.global get_data_end_addr
get_data_end_addr:
    ldr x0, =__data_end
    ret

// get_bss_start_addr() returns uintptr
.global get_bss_start_addr
get_bss_start_addr:
    ldr x0, =__bss_start
    ret

// get_bss_end_addr() returns uintptr
.global get_bss_end_addr
get_bss_end_addr:
    ldr x0, =__bss_end
    ret

// get_end_addr() returns uintptr
.global get_end_addr
get_end_addr:
    ldr x0, =__end
    ret

// get_stack_top_addr() returns uintptr
.global get_stack_top_addr
get_stack_top_addr:
    ldr x0, =__stack_top
    ret

// get_page_tables_start_addr() returns uintptr
.global get_page_tables_start_addr
get_page_tables_start_addr:
    ldr x0, =__page_tables_start
    ret

// get_page_tables_end_addr() returns uintptr
.global get_page_tables_end_addr
get_page_tables_end_addr:
    ldr x0, =__page_tables_end
    ret

// get_ram_start() returns uintptr
// Returns QEMU's physical RAM base address (platform-specific)
.global get_ram_start
get_ram_start:
    ldr x0, =__ram_start
    ret

// get_dtb_boot_addr() returns uintptr
// Returns QEMU's DTB location (platform-specific, not part of relocatable layout)
.global get_dtb_boot_addr
get_dtb_boot_addr:
    ldr x0, =__dtb_boot_addr
    ret

// get_dtb_size() returns uintptr
// Returns DTB size (1MB reserved by QEMU)
.global get_dtb_size
get_dtb_size:
    ldr x0, =__dtb_size
    ret

// get_mazboot_end() returns uintptr
// Returns end of mazboot 15MB allocation (where kmazarin starts)
.global get_mazboot_end
get_mazboot_end:
    ldr x0, =__mazboot_end
    ret

// get_mazboot_allocation_size() returns uintptr
// Returns total mazboot allocation size (15MB)
.global get_mazboot_allocation_size
get_mazboot_allocation_size:
    ldr x0, =__mazboot_allocation_size
    ret

// get_kmazarin_load_addr() returns uintptr
// Returns virtual address where kmazarin should be loaded
.global get_kmazarin_load_addr
get_kmazarin_load_addr:
    ldr x0, =__kmazarin_load_addr
    ret

// get_g0_stack_bottom() returns uintptr
// Returns bottom of g0 stack (32KB below stack top)
.global get_g0_stack_bottom
get_g0_stack_bottom:
    ldr x0, =__g0_stack_bottom
    ret

// MMIO device addresses (QEMU virt machine)

// get_gic_base() returns uintptr
.global get_gic_base
get_gic_base:
    ldr x0, =__gic_base
    ret

// get_gic_size() returns uintptr
.global get_gic_size
get_gic_size:
    ldr x0, =__gic_size
    ret

// get_uart_base() returns uintptr
.global get_uart_base
get_uart_base:
    ldr x0, =__uart_base
    ret

// get_uart_size() returns uintptr
.global get_uart_size
get_uart_size:
    ldr x0, =__uart_size
    ret

// get_rtc_base() returns uintptr
.global get_rtc_base
get_rtc_base:
    ldr x0, =__rtc_base
    ret

// get_fwcfg_base() returns uintptr
.global get_fwcfg_base
get_fwcfg_base:
    ldr x0, =__fwcfg_base
    ret

// get_fwcfg_size() returns uintptr
.global get_fwcfg_size
get_fwcfg_size:
    ldr x0, =__fwcfg_size
    ret

// get_bochs_display_base() returns uintptr
.global get_bochs_display_base
get_bochs_display_base:
    ldr x0, =__bochs_display_base
    ret

// get_bochs_display_size() returns uintptr
.global get_bochs_display_size
get_bochs_display_size:
    ldr x0, =__bochs_display_size
    ret

// get_pci_bar_base() returns uintptr
.global get_pci_bar_base
get_pci_bar_base:
    ldr x0, =__pci_bar_base
    ret

// get_pci_bar_size() returns uintptr
.global get_pci_bar_size
get_pci_bar_size:
    ldr x0, =__pci_bar_size
    ret

// Embedded kmazarin kernel symbols

// get_kmazarin_start() returns uintptr
.global get_kmazarin_start
get_kmazarin_start:
    ldr x0, =__kmazarin_start
    ret

// get_kmazarin_size() returns uintptr
.global get_kmazarin_size
get_kmazarin_size:
    ldr x0, =__kmazarin_size
    ret

// Declare runtime TLS functions as weak so linker doesn't fail if they're not yet visible
// Note: Go uses .abi0 suffix for ABI wrappers
.weak runtime.load_g.abi0
.weak runtime.save_g.abi0

// Declare runtime.mheap_ global variable as external reference
// This ensures it gets added to globalize_symbols.txt and properly linked
.extern runtime.mheap_

// get_runtime_mheap_addr() returns uintptr
// Returns the address of runtime.mheap_ global variable
.global get_runtime_mheap_addr
get_runtime_mheap_addr:
    ldr x0, =runtime.mheap_
    ret

// get_runtime_load_g_addr() returns uintptr
.global get_runtime_load_g_addr
get_runtime_load_g_addr:
    ldr x0, =runtime.load_g.abi0
    ret

// get_runtime_save_g_addr() returns uintptr
.global get_runtime_save_g_addr
get_runtime_save_g_addr:
    ldr x0, =runtime.save_g.abi0
    ret
