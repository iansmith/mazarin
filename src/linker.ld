ENTRY(_start)
 
SECTIONS
{
    /* QEMU virt machine memory layout (1GB RAM):
     * 0x00000000 - 0x08000000: Flash/ROM region (kernel loaded here)
     * 0x09000000 - 0x09010000: UART PL011
     * 0x40000000 - 0x40100000: DTB (QEMU device tree blob, 1MB)
     * 0x40100000 - 0x60000000: Kernel RAM (512MB allocated for kernel development)
     *   0x40100000 - 0x401xxxxx: BSS section
     *   0x40400000 - 0x60000000: Stack (grows downward from 0x60000000)
     *   0x40500000 - 0x60000000: Heap (after stack region)
     * 0x60000000 - 0x80000000: Reserved (not used by kernel)
     */
    
    /* Kernel code/data in ROM region */
    . = 0x200000;
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
 
    __data_start = .;
    .data :
    {
        *(.data)
    }
    . = ALIGN(4096);
    __data_end = .;
 
    /* BSS in RAM region (0x40000000+) */
    /* IMPORTANT: QEMU virt machine places DTB at 0x40000000-0x40100000 (1MB)
     * We must start BSS after the DTB to avoid overlap.
     * Place BSS at 0x40100000 (right after DTB region)
     * Kernel has 512MB total (0x40100000 - 0x60000000) */
    . = 0x40100000;
    __bss_start = .;
    .bss (NOLOAD) :
    {
        bss = .;
        *(.bss)
        *(.bss.*)
        *(COMMON)
        /* Include .noptrbss here to avoid gaps */
        *(.noptrbss)
        *(.noptrbss.*)
    }
    . = ALIGN(4096);
    __bss_end = .;
    __end = .;
    
    /* Stack region: 512MB - 16MB = 496MB available for stack
     * Stack grows downward from 0x60000000 (top of kernel RAM)
     * Stack bottom moved to 0x41000000 (16MB into RAM) to leave room for:
     *   - BSS section (~1-2MB)
     *   - Page metadata array (~3-6MB for 512MB-1GB RAM)
     *   - Heap (64MB, but may be limited)
     * This gives us plenty of space for kernel development */
    . = 0x60000000;
    __stack_top = .;
    __stack_bottom = 0x41000000;  /* Stack bottom (16MB into RAM, leaves room for page array + heap) */
}