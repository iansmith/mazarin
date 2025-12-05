ENTRY(_start)
 
SECTIONS
{
    /* QEMU virt machine memory layout:
     * 0x00000000 - 0x08000000: Flash/ROM region (kernel loaded here)
     * 0x09000000 - 0x09010000: UART PL011
     * 0x40000000 - 0x40100000: DTB (QEMU device tree blob, 1MB)
     * 0x40100000 - end:         RAM (writable memory for BSS/stack/heap)
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
     * Place BSS at 0x40100000 (right after DTB region) */
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
}