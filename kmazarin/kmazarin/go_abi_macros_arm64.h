// go_abi_macros_arm64.h - Go ABI0 calling convention macros for ARM64 assembly
//
// These macros simplify calling Go functions from raw assembly (e.g. exception
// handlers) using Go's ABI0 stack-based calling convention. On ARM64, BL does
// not push a return address to the stack (it goes to LR), so arguments start
// at 8(RSP) — the first 8 bytes at 0(RSP) are reserved for the callee's
// linkage.
//
// Usage: GO_CALL_N_M(function, arg_regs...)
//   N = number of arguments
//   M = number of returns (0 or 1)
//   arg_regs = registers containing the arguments
//   Return value (if M=1) is in R0 after the macro completes.
//
// IMPORTANT: These macros clobber R0-R17 (caller-saved registers per Go ABI).
// Preserve any needed values in callee-saved registers (R19-R28) before calling.
// The macros also temporarily modify RSP (restored before return).

// GO_CALL_0_1(fn) - 0 args, 1 return in R0
// Frame: 16 bytes (8 reserved + 8 return)
#define GO_CALL_0_1(fn) \
	SUB	$16, RSP; \
	CALL	fn(SB); \
	MOVD	8(RSP), R0; \
	ADD	$16, RSP

// GO_CALL_1_0(fn, a0) - 1 arg, 0 returns
// Frame: 16 bytes (8 reserved + 8 arg)
#define GO_CALL_1_0(fn, a0) \
	SUB	$16, RSP; \
	MOVD	a0, 8(RSP); \
	CALL	fn(SB); \
	ADD	$16, RSP

// GO_CALL_1_1(fn, a0) - 1 arg, 1 return in R0
// Frame: 32 bytes (8 reserved + 8 arg + 8 return, padded to 16-align)
#define GO_CALL_1_1(fn, a0) \
	SUB	$32, RSP; \
	MOVD	a0, 8(RSP); \
	CALL	fn(SB); \
	MOVD	16(RSP), R0; \
	ADD	$32, RSP

// GO_CALL_2_1(fn, a0, a1) - 2 args, 1 return in R0
// Frame: 32 bytes (8 reserved + 16 args + 8 return = 32)
#define GO_CALL_2_1(fn, a0, a1) \
	SUB	$32, RSP; \
	MOVD	a0, 8(RSP); \
	MOVD	a1, 16(RSP); \
	CALL	fn(SB); \
	MOVD	24(RSP), R0; \
	ADD	$32, RSP

// GO_CALL_4_0(fn, a0, a1, a2, a3) - 4 args, 0 returns
// Frame: 48 bytes (8 reserved + 32 args, padded to 16-align)
#define GO_CALL_4_0(fn, a0, a1, a2, a3) \
	SUB	$48, RSP; \
	MOVD	a0, 8(RSP); \
	MOVD	a1, 16(RSP); \
	MOVD	a2, 24(RSP); \
	MOVD	a3, 32(RSP); \
	CALL	fn(SB); \
	ADD	$48, RSP

// GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) - 7 args, 1 return in R0
// Frame: 80 bytes (8 reserved + 56 args + 8 return = 72, padded to 16-align)
#define GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) \
	SUB	$80, RSP; \
	MOVD	a0, 8(RSP); \
	MOVD	a1, 16(RSP); \
	MOVD	a2, 24(RSP); \
	MOVD	a3, 32(RSP); \
	MOVD	a4, 40(RSP); \
	MOVD	a5, 48(RSP); \
	MOVD	a6, 56(RSP); \
	CALL	fn(SB); \
	MOVD	64(RSP), R0; \
	ADD	$80, RSP
