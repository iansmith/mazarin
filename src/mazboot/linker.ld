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
 
    /* BSS in RAM region (0x40000000+) */
    /* IMPORTANT: QEMU virt machine places DTB at 0x40000000-0x40100000 (1MB)
     * We must start BSS after the DTB to avoid overlap.
     * Place BSS at 0x40100000 (right after DTB region)
     * Kernel has 128MB total (0x40100000 - 0x48100000) */
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
    
    /* g0 stack region: 8KB at top of kernel RAM
     * g0 stack: 0x5EFFFE000 - 0x5F000000 (8KB, system goroutine)
     * Stack grows downward from 0x5F000000 (top) to 0x5EFFFE000 (bottom)
     * Main goroutine stack: 32KB (allocated from heap)
     * Heap extends from __end to 0x5EFFFE000 (just before g0 stack) */
    . = 0x5F000000;
    __stack_top = .;
    __g0_stack_bottom = 0x5EFFFE000;  /* g0 stack bottom (8KB below top) */
}