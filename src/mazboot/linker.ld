ENTRY(_start)
 
SECTIONS
{
    /* QEMU virt machine memory layout (1GB RAM):
     * 0x00000000 - 0x08000000: Flash/ROM region (kernel loaded here)
     * 0x09000000 - 0x09010000: UART PL011
     * 0x40000000 - 0x40100000: DTB (QEMU device tree blob, 1MB)
     * 0x40100000 - 0x48100000: Kernel RAM (128MB allocated for kernel development)
     *   0x40100000 - 0x401xxxxx: BSS section
     *   0x401xxxxx - 0x5FFFFE000: Heap (extends to g0 stack)
     *   0x5FFFFE000 - 0x5F000000: g0 stack (8KB, system goroutine)
     *   0x5F000000: Stack top (SP for g0)
     * 0x48100000 - 0x80000000: Reserved (not used by kernel)
     */
    
    /* QEMU virt machine platform constants
     * These addresses are fixed by QEMU hardware, not part of our relocatable layout
     */
    PROVIDE(__ram_start = 0x40000000);      /* Physical RAM base address */
    PROVIDE(__dtb_boot_addr = 0x40000000);  /* DTB location (start of RAM) */
    PROVIDE(__dtb_size = 0x100000);         /* DTB size: 1MB (QEMU reserves this) */

    /* QEMU virt machine MMIO device addresses */
    PROVIDE(__gic_base = 0x08000000);       /* GIC (Generic Interrupt Controller) */
    PROVIDE(__gic_size = 0x00020000);       /* GIC size: 128KB (dist + cpu interface) */
    PROVIDE(__uart_base = 0x09000000);      /* UART PL011 */
    PROVIDE(__uart_size = 0x00010000);      /* UART size: 64KB */
    PROVIDE(__rtc_base = 0x09010000);       /* PL031 RTC */
    PROVIDE(__fwcfg_base = 0x09020000);     /* QEMU fw_cfg device */
    PROVIDE(__fwcfg_size = 0x00010000);     /* fw_cfg size: 64KB */
    PROVIDE(__bochs_display_base = 0x10000000);  /* bochs-display framebuffer */
    PROVIDE(__bochs_display_size = 0x01000000);  /* bochs-display size: 16MB */

    /* Kernel code/data in ROM region
     *
     * NOTE: We start the kernel at 0x300000 instead of 0x200000 to leave room
     * for a DTB that QEMU may place at the bottom of physical address space.
     * Some QEMU configurations (especially when using -dtb) load the DTB at
     * 0x0..~0x200000; placing the kernel at 0x300000 avoids overlap.
     */
    . = 0x300000;
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

    __rodata_start = .;
    .rodata :
    {
        *(.rodata)
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
 
    __data_start = .;
    .data :
    {
        *(.data)
    }
    . = ALIGN(4096);
    __data_end = .;
 
    /* Writable data sections in RAM region (0x40000000+) */
    /* IMPORTANT: QEMU virt machine places DTB at 0x40000000-0x40100000 (1MB)
     * Kernel has 128MB total (0x40100000 - 0x48100000)
     *
     * NOTE: All runtime addresses use linker symbols (not hardcoded), so
     * we can safely place .noptrdata before .bss without breaking anything.
     */
    . = 0x40100000;
    
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
    
    /* g0 stack region: 32KB at top of kernel RAM
     * g0 stack: 0x5EFF8000 - 0x5F000000 (32KB, system goroutine)
     * Stack grows downward from 0x5F000000 (top) to 0x5EFF8000 (bottom)
     * Main goroutine stack: 32KB (allocated from heap)
     * Heap extends from __end to 0x5EFF8000 (just before g0 stack) */
    . = 0x5F000000;
    __stack_top = .;
    __g0_stack_bottom = 0x5EFF8000;  /* g0 stack bottom (32KB below top) */

    /* Page tables region: 15MB reserved for MMU page tables
     * Located at 0x5F100000 - 0x60000000 (15MB)
     * Leaves 1MB safety margin after g0 stack (0x5F000000 - 0x5F100000)
     * Page tables are allocated at runtime, not by linker */
    __page_tables_start = 0x5F100000;
    __page_tables_end = 0x60000000;
    __page_tables_size = __page_tables_end - __page_tables_start;  /* 15MB */
}