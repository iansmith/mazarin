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

// get_g0_stack_bottom() returns uintptr
// Returns bottom of g0 stack (32KB below stack top)
.global get_g0_stack_bottom
get_g0_stack_bottom:
    ldr x0, =__g0_stack_bottom
    ret
