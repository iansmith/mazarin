ENTRY(_start)
 
SECTIONS
{
    /* QEMU virt machine memory layout:
     * 0x00000000 - 0x08000000: Flash/ROM region (kernel loaded here)
     * 0x09000000 - 0x09010000: UART PL011
     * 0x40000000 - end:         RAM (writable memory)
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
    /* Place BSS at start of RAM for simplicity */
    . = 0x40000000;
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