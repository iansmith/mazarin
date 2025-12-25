ENTRY(_start)

SECTIONS
{
    /* ============================================================================
     * MEMORY LAYOUT BASE ADDRESS - SINGLE SOURCE OF TRUTH
     * ============================================================================
     * To relocate the entire kernel to a different virtual address (e.g., higher
     * half kernel at 0xFFFF000000000000), change ONLY this value. All other
     * addresses are calculated relative to BOOT_ADDRESS.
     */
    BOOT_ADDRESS = 0x40000000;

    /* ============================================================================
     * ALLOCATION SIZES - CONFIGURED HERE
     * ============================================================================ */
    DTB_SIZE = 0x100000;                    /* 1 MB for Device Tree Blob */
    ALLOCATION_SIZE_MAZBOOT = 0xF00000;     /* 15 MB for mazboot (code+data+bss+heap) */
    PAGE_TABLE_SIZE = 0x800000;             /* 8 MB for page tables (L0/L1/L2/L3) */

    /* ============================================================================
     * CALCULATED MEMORY BOUNDARIES - DO NOT HARDCODE ADDRESSES
     * ============================================================================ */
    DTB_START = BOOT_ADDRESS;
    DTB_END = DTB_START + DTB_SIZE;

    MAZBOOT_START = DTB_END;                /* Mazboot starts after DTB */
    MAZBOOT_END = MAZBOOT_START + ALLOCATION_SIZE_MAZBOOT;

    PAGE_TABLE_START = MAZBOOT_END;         /* Page tables start after mazboot */
    PAGE_TABLE_END = PAGE_TABLE_START + PAGE_TABLE_SIZE;

    KMAZARIN_START = PAGE_TABLE_END;        /* Kmazarin starts after page tables */

    /* ============================================================================
     * QEMU VIRT MACHINE MMIO DEVICE ADDRESSES
     * These are PHYSICAL addresses fixed by QEMU hardware, independent of VA layout
     * ============================================================================ */
    PROVIDE(__gic_base = 0x08000000);       /* GIC (Generic Interrupt Controller) */
    PROVIDE(__gic_size = 0x00020000);       /* GIC size: 128KB (dist + cpu interface) */
    PROVIDE(__uart_base = 0x09000000);      /* UART PL011 */
    PROVIDE(__uart_size = 0x00010000);      /* UART size: 64KB */
    PROVIDE(__rtc_base = 0x09010000);       /* PL031 RTC */
    PROVIDE(__fwcfg_base = 0x09020000);     /* QEMU fw_cfg device */
    PROVIDE(__fwcfg_size = 0x00010000);     /* fw_cfg size: 64KB */
    PROVIDE(__bochs_display_base = 0x10000000);  /* bochs-display framebuffer */
    PROVIDE(__bochs_display_size = 0x01000000);  /* bochs-display size: 16MB */

    /* ============================================================================
     * EXPORTED SYMBOLS FOR RUNTIME USE
     * ============================================================================ */
    PROVIDE(__ram_start = BOOT_ADDRESS);
    PROVIDE(__dtb_boot_addr = DTB_START);
    PROVIDE(__dtb_size = DTB_SIZE);
    PROVIDE(__mazboot_start = MAZBOOT_START);
    PROVIDE(__mazboot_end = MAZBOOT_END);
    PROVIDE(__mazboot_allocation_size = ALLOCATION_SIZE_MAZBOOT);
    PROVIDE(__page_table_start = PAGE_TABLE_START);
    PROVIDE(__page_table_end = PAGE_TABLE_END);
    PROVIDE(__page_table_size = PAGE_TABLE_SIZE);
    PROVIDE(__kmazarin_load_addr = KMAZARIN_START);

    /* ============================================================================
     * MAZBOOT KERNEL SECTIONS
     * ============================================================================ */
    . = MAZBOOT_START;
    __start = .;
    __text_start = .;
    .text :
    {
        KEEP(*(.text.boot))
        *(.text)
    }
    . = ALIGN(4096);
    __text_end = .;

    /* Exception vector table - must be 2KB (0x800) aligned */
    /* Place exception vectors after kernel text */
    . = ALIGN(0x800);  /* 2KB align */
    PROVIDE(exception_vectors_start = .);
    .vectors :
    {
        *(.vectors)
        *(.text.exceptions)
    }
    . = ALIGN(0x800);
    PROVIDE(exception_vectors_end = .);

    /* Align .rodata to 16 bytes for efficient ldp/stp access */
    . = ALIGN(16);
    __rodata_start = .;
    .rodata : ALIGN(16) SUBALIGN(16)
    {
        *(.rodata .rodata.*)
    }
    . = ALIGN(4096);
    __rodata_end = .;

    /* Embedded image data */
    __image_data_start = .;
    .imagedata :
    {
        KEEP(*(.imagedata))
    }
    . = ALIGN(4096);
    __image_data_end = .;

    /* Embedded kmazarin kernel binary
     * This section contains the kmazarin.elf binary embedded in mazboot
     * The kernel will be copied from here to its execution address at runtime
     */
    . = ALIGN(4096);
    __kmazarin_start = .;
    .kmazarin :
    {
        KEEP(*(.kmazarin))
    }
    . = ALIGN(4096);
    __kmazarin_end = .;
    __kmazarin_size = __kmazarin_end - __kmazarin_start;

    /* Writable data sections continue in RAM */
    . = ALIGN(4096);

    /* .data: initialized globals WITH pointers (must be writable) */
    __data_start = .;
    .data : ALIGN(16) SUBALIGN(16)
    {
        *(.data .data.*)
    }
    . = ALIGN(16);
    __data_end = .;

    /* Go's .noptrdata: initialized globals without pointers (must be writable) */
    __noptrdata_start = .;
    .noptrdata : ALIGN(16)
    {
        *(.noptrdata)
        *(.noptrdata.*)
    }
    . = ALIGN(16);
    __noptrdata_end = .;
    
    /* BSS: zero-initialized data (cleared by boot.s) */
    __bss_start = .;
    .bss (NOLOAD) :
    {
        bss = .;
        *(.bss)
        *(.bss.*)
        *(COMMON)
        *(.noptrbss)
        *(.noptrbss.*)
    }
    . = ALIGN(4096);
    __bss_end = .;
    __end = .;

    /* ============================================================================
     * KMALLOC HEAP REGION
     * ============================================================================
     * Heap starts immediately after BSS (page-aligned) and extends to mazboot_end.
     * The kmalloc/kfree allocator uses this region for dynamic allocations.
     * Heap boundaries are calculated at runtime in Go code using:
     *   - heap_start = (__bss_end + 4095) & ~4095  (page-aligned)
     *   - heap_end = __mazboot_end
     *   - heap_size = heap_end - heap_start
     */
    PROVIDE(__heap_start_hint = ALIGN(., 4096));  /* Suggested heap start (page-aligned) */

    /* Mazboot allocation ends here - heap extends from __heap_start_hint to __mazboot_end */
    ASSERT(. <= MAZBOOT_END, "ERROR: Mazboot sections exceed 15MB allocation! Increase ALLOCATION_SIZE_MAZBOOT")

    /* ============================================================================
     * LEGACY SYMBOLS FOR BACKWARD COMPATIBILITY
     * ============================================================================
     * These symbols exist for compatibility with existing code that references them.
     * They are NOT used for actual allocation - everything is calculated dynamically.
     */
    /* Note: The following are PLACEHOLDER values only - actual values calculated at runtime:
     *   - g0 stack: Allocated dynamically from heap
     *   - Stack top: Depends on g0 stack allocation
     */
    PROVIDE(__stack_top = 0);           /* Placeholder - g0 stack top (calculated at runtime) */
    PROVIDE(__g0_stack_bottom = 0);     /* Placeholder - g0 stack bottom (calculated at runtime) */

    /* Page table symbols now point to calculated values (no longer placeholders) */
    PROVIDE(__page_tables_start = PAGE_TABLE_START);
    PROVIDE(__page_tables_end = PAGE_TABLE_END);
}