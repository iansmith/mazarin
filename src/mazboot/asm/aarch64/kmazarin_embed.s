/*
 * kmazarin_embed.s - Embed kmazarin kernel binary into mazboot
 *
 * This file embeds the kmazarin.elf binary as raw data in a special section.
 * The linker script provides __kmazarin_start and __kmazarin_end symbols
 * that can be used to locate and copy the embedded kernel at runtime.
 */

.section .kmazarin, "a"
.balign 4096

.global kmazarin_binary_start
kmazarin_binary_start:
    .incbin "src/kmazarin/build/kmazarin.elf"
.global kmazarin_binary_end
kmazarin_binary_end:

.balign 4096
