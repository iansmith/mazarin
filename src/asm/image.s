.section .imagedata, "a"
.global _binary_boot_mazarin_bin_start
.global _binary_boot_mazarin_bin_end
.global _binary_boot_mazarin_bin_size

.align 4
_binary_boot_mazarin_bin_start:
.incbin "/Users/iansmith/mazzy/assets/boot-mazarin.bin"
_binary_boot_mazarin_bin_end:

.set _binary_boot_mazarin_bin_size, _binary_boot_mazarin_bin_end - _binary_boot_mazarin_bin_start

// Assembly functions to load the image data addresses
.section .text

// imageDataStart returns the address of the image data start
// No parameters, returns x0 = address
.global imageDataStart
.align 2
imageDataStart:
	adrp x0, _binary_boot_mazarin_bin_start
	add x0, x0, :lo12:_binary_boot_mazarin_bin_start
	ret

// imageDataEnd returns the address of the image data end
// No parameters, returns x0 = address
.global imageDataEnd
.align 2
imageDataEnd:
	adrp x0, _binary_boot_mazarin_bin_end
	add x0, x0, :lo12:_binary_boot_mazarin_bin_end
	ret


