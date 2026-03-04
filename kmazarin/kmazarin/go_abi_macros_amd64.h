// go_abi_macros_amd64.h - Go ABI0 calling convention macros for AMD64 assembly
//
// These macros simplify calling Go functions from raw assembly (e.g. exception
// handlers) using Go's ABI0 stack-based calling convention. On AMD64, CALL
// pushes an 8-byte return address to the stack, so arguments placed at 0(SP)
// before CALL appear at 8(SP) from the callee's perspective.
//
// Usage: GO_CALL_N_M(function, arg_regs...)
//   N = number of arguments
//   M = number of returns (0 or 1)
//   arg_regs = registers containing the arguments
//   Return value (if M=1) is in AX after the macro completes.
//
// IMPORTANT: These macros clobber AX, CX, DX, DI, SI, R8-R11 (caller-saved).
// Preserve any needed values in callee-saved registers (BX, BP, R12-R15) first.
// The macros also temporarily modify SP (restored before return).

// GO_CALL_0_0(fn) - 0 args, 0 returns
// Frame: 16 bytes (padding for 16-byte alignment)
#define GO_CALL_0_0(fn) \
	SUBQ	$16, SP; \
	CALL	fn(SB); \
	ADDQ	$16, SP

// GO_CALL_0_1(fn) - 0 args, 1 return in AX
// Frame: 16 bytes (8 return + 8 padding for alignment)
#define GO_CALL_0_1(fn) \
	SUBQ	$16, SP; \
	CALL	fn(SB); \
	MOVQ	0(SP), AX; \
	ADDQ	$16, SP

// GO_CALL_1_0(fn, a0) - 1 arg, 0 returns
// Frame: 16 bytes (8 arg + 8 padding for alignment)
#define GO_CALL_1_0(fn, a0) \
	SUBQ	$16, SP; \
	MOVQ	a0, 0(SP); \
	CALL	fn(SB); \
	ADDQ	$16, SP

// GO_CALL_1_1(fn, a0) - 1 arg, 1 return in AX
// Frame: 16 bytes (8 arg + 8 return)
#define GO_CALL_1_1(fn, a0) \
	SUBQ	$16, SP; \
	MOVQ	a0, 0(SP); \
	CALL	fn(SB); \
	MOVQ	8(SP), AX; \
	ADDQ	$16, SP

// GO_CALL_2_1(fn, a0, a1) - 2 args, 1 return in AX
// Frame: 32 bytes (16 args + 8 return, padded to 16-align)
#define GO_CALL_2_1(fn, a0, a1) \
	SUBQ	$32, SP; \
	MOVQ	a0, 0(SP); \
	MOVQ	a1, 8(SP); \
	CALL	fn(SB); \
	MOVQ	16(SP), AX; \
	ADDQ	$32, SP

// GO_CALL_3_0(fn, a0, a1, a2) - 3 args, 0 returns
// Frame: 32 bytes (24 args + 8 padding for alignment)
#define GO_CALL_3_0(fn, a0, a1, a2) \
	SUBQ	$32, SP; \
	MOVQ	a0, 0(SP); \
	MOVQ	a1, 8(SP); \
	MOVQ	a2, 16(SP); \
	CALL	fn(SB); \
	ADDQ	$32, SP

// GO_CALL_3_1(fn, a0, a1, a2) - 3 args, 1 return in AX
// Frame: 32 bytes (24 args + 8 return)
#define GO_CALL_3_1(fn, a0, a1, a2) \
	SUBQ	$32, SP; \
	MOVQ	a0, 0(SP); \
	MOVQ	a1, 8(SP); \
	MOVQ	a2, 16(SP); \
	CALL	fn(SB); \
	MOVQ	24(SP), AX; \
	ADDQ	$32, SP

// GO_CALL_4_0(fn, a0, a1, a2, a3) - 4 args, 0 returns
// Frame: 64 bytes (32 args + 32 return space for callee safety)
// Note: some 4-arg functions (e.g. TimerIRQHandler) have return values in their
// Go signature even though the caller ignores them. We allocate the full 64
// bytes to prevent the callee's .abi0 wrapper from writing past our frame.
#define GO_CALL_4_0(fn, a0, a1, a2, a3) \
	SUBQ	$64, SP; \
	MOVQ	a0, 0(SP); \
	MOVQ	a1, 8(SP); \
	MOVQ	a2, 16(SP); \
	MOVQ	a3, 24(SP); \
	CALL	fn(SB); \
	ADDQ	$64, SP

// GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) - 7 args, 1 return in AX
// Frame: 64 bytes (56 args + 8 return)
#define GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) \
	SUBQ	$64, SP; \
	MOVQ	a0, 0(SP); \
	MOVQ	a1, 8(SP); \
	MOVQ	a2, 16(SP); \
	MOVQ	a3, 24(SP); \
	MOVQ	a4, 32(SP); \
	MOVQ	a5, 40(SP); \
	MOVQ	a6, 48(SP); \
	CALL	fn(SB); \
	MOVQ	56(SP), AX; \
	ADDQ	$64, SP
