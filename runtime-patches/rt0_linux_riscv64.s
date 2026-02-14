// KMAZARIN OVERLAY: rt0_linux_riscv64.s with debug markers for re-entry detection.
// This replaces the standard Go runtime entry point for RISC-V Linux.
// Added: UART markers at _rt0_riscv64_linux ('Z') and main ('z') to detect
// if the entry point is called more than once.
//
// RISC-V QEMU virt UART NS16550 at VA 0xFFFFFFFF10000000 (via linear map)

#include "textflag.h"

TEXT _rt0_riscv64_linux(SB),NOSPLIT|NOFRAME,$0
	// Debug: write 'Z' to UART to detect entry/re-entry
	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x5A, X29	// 'Z'
	MOVB	X29, (X28)
	// Print SP: "Z<16hex>"
	MOV	X2, X30		// copy SP
	MOV	$60, X31	// bit position
z_sp_hex:
	SRL	X31, X30, X29
	AND	$0xF, X29
	MOV	$10, X28
	BLT	X29, X28, z_sp_digit
	ADD	$0x37, X29, X29	// A-F
	JMP	z_sp_out
z_sp_digit:
	ADD	$0x30, X29, X29	// 0-9
z_sp_out:
	MOV	$0xFFFFFFFF10000000, X28
	MOVB	X29, (X28)
	ADD	$-4, X31
	MOV	$-4, X28
	BNE	X31, X28, z_sp_hex

	// Print RA (X1): "r<16hex>"
	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x72, X29	// 'r'
	MOVB	X29, (X28)
	MOV	X1, X30		// copy RA
	MOV	$60, X31
z_ra_hex:
	SRL	X31, X30, X29
	AND	$0xF, X29
	MOV	$10, X28
	BLT	X29, X28, z_ra_digit
	ADD	$0x37, X29, X29
	JMP	z_ra_out
z_ra_digit:
	ADD	$0x30, X29, X29
z_ra_out:
	MOV	$0xFFFFFFFF10000000, X28
	MOVB	X29, (X28)
	ADD	$-4, X31
	MOV	$-4, X28
	BNE	X31, X28, z_ra_hex

	MOV	0(X2), A0	// argc
	ADD	$8, X2, A1	// argv
	JMP	main(SB)

// When building with -buildmode=c-shared, this symbol is called when the shared
// library is loaded.
TEXT _rt0_riscv64_linux_lib(SB),NOSPLIT,$224
	// Preserve callee-save registers, along with X1 (LR).
	MOV	X1, (8*3)(X2)
	MOV	X8, (8*4)(X2)
	MOV	X9, (8*5)(X2)
	MOV	X18, (8*6)(X2)
	MOV	X19, (8*7)(X2)
	MOV	X20, (8*8)(X2)
	MOV	X21, (8*9)(X2)
	MOV	X22, (8*10)(X2)
	MOV	X23, (8*11)(X2)
	MOV	X24, (8*12)(X2)
	MOV	X25, (8*13)(X2)
	MOV	X26, (8*14)(X2)
	MOV	g, (8*15)(X2)
	MOVD	F8, (8*16)(X2)
	MOVD	F9, (8*17)(X2)
	MOVD	F18, (8*18)(X2)
	MOVD	F19, (8*19)(X2)
	MOVD	F20, (8*20)(X2)
	MOVD	F21, (8*21)(X2)
	MOVD	F22, (8*22)(X2)
	MOVD	F23, (8*23)(X2)
	MOVD	F24, (8*24)(X2)
	MOVD	F25, (8*25)(X2)
	MOVD	F26, (8*26)(X2)
	MOVD	F27, (8*27)(X2)

	// Initialize g as nil in case of using g later e.g. sigaction in cgo_sigaction.go
	MOV	X0, g

	MOV	A0, _rt0_riscv64_linux_lib_argc<>(SB)
	MOV	A1, _rt0_riscv64_linux_lib_argv<>(SB)

	// Synchronous initialization.
	MOV	$runtime·libpreinit(SB), T0
	JALR	RA, T0

	// Create a new thread to do the runtime initialization and return.
	MOV	_cgo_sys_thread_create(SB), T0
	BEQZ	T0, nocgo
	MOV	$_rt0_riscv64_linux_lib_go(SB), A0
	MOV	$0, A1
	JALR	RA, T0
	JMP	restore

nocgo:
	MOV	$0x800000, A0                     // stacksize = 8192KB
	MOV	$_rt0_riscv64_linux_lib_go(SB), A1
	MOV	A0, 8(X2)
	MOV	A1, 16(X2)
	MOV	$runtime·newosproc0(SB), T0
	JALR	RA, T0

restore:
	// Restore callee-save registers, along with X1 (LR).
	MOV	(8*3)(X2), X1
	MOV	(8*4)(X2), X8
	MOV	(8*5)(X2), X9
	MOV	(8*6)(X2), X18
	MOV	(8*7)(X2), X19
	MOV	(8*8)(X2), X20
	MOV	(8*9)(X2), X21
	MOV	(8*10)(X2), X22
	MOV	(8*11)(X2), X23
	MOV	(8*12)(X2), X24
	MOV	(8*13)(X2), X25
	MOV	(8*14)(X2), X26
	MOV	(8*15)(X2), g
	MOVD	(8*16)(X2), F8
	MOVD	(8*17)(X2), F9
	MOVD	(8*18)(X2), F18
	MOVD	(8*19)(X2), F19
	MOVD	(8*20)(X2), F20
	MOVD	(8*21)(X2), F21
	MOVD	(8*22)(X2), F22
	MOVD	(8*23)(X2), F23
	MOVD	(8*24)(X2), F24
	MOVD	(8*25)(X2), F25
	MOVD	(8*26)(X2), F26
	MOVD	(8*27)(X2), F27

	RET

TEXT _rt0_riscv64_linux_lib_go(SB),NOSPLIT,$0
	MOV	_rt0_riscv64_linux_lib_argc<>(SB), A0
	MOV	_rt0_riscv64_linux_lib_argv<>(SB), A1
	MOV	$runtime·rt0_go(SB), T0
	JALR	ZERO, T0

DATA _rt0_riscv64_linux_lib_argc<>(SB)/8, $0
GLOBL _rt0_riscv64_linux_lib_argc<>(SB),NOPTR, $8
DATA _rt0_riscv64_linux_lib_argv<>(SB)/8, $0
GLOBL _rt0_riscv64_linux_lib_argv<>(SB),NOPTR, $8


TEXT main(SB),NOSPLIT|NOFRAME,$0
	// Debug: write 'z' to UART
	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x7A, X29	// 'z'
	MOVB	X29, (X28)

	MOV	$runtime·rt0_go(SB), T0
	JALR	ZERO, T0
