// go_abi_macros_riscv64.h - Go ABI0 calling convention macros for RISC-V assembly
//
// These macros simplify calling Go functions from raw assembly (e.g. trap
// handlers) using Go's ABI0 stack-based calling convention. On RISC-V, CALL
// (JALR) stores the return address in RA (X1), not on the stack, so arguments
// start at 8(X2) — the first 8 bytes at 0(X2) are reserved for the callee's
// linkage.
//
// Usage: GO_CALL_N_M(function, arg_regs...)
//   N = number of arguments
//   M = number of returns (0 or 1)
//   arg_regs = registers containing the arguments
//   Return value (if M=1) is in T0 (X5) after the macro completes.
//
// IMPORTANT: These macros clobber T0-T6 and A0-A7 (caller-saved registers).
// Preserve any needed values in S-registers (S0-S11) before calling.
// The macros also temporarily modify X2/SP (restored before return).

// GO_CALL_0_1(fn) - 0 args, 1 return in T0
// Frame: 16 bytes (8 reserved + 8 return)
#define GO_CALL_0_1(fn) \
	ADD	$-16, X2; \
	CALL	fn(SB); \
	MOV	8(X2), T0; \
	ADD	$16, X2

// GO_CALL_1_0(fn, a0) - 1 arg, 0 returns
// Frame: 16 bytes (8 reserved + 8 arg)
#define GO_CALL_1_0(fn, a0) \
	ADD	$-16, X2; \
	MOV	a0, 8(X2); \
	CALL	fn(SB); \
	ADD	$16, X2

// GO_CALL_1_1(fn, a0) - 1 arg, 1 return in T0
// Frame: 24 bytes (8 reserved + 8 arg + 8 return)
#define GO_CALL_1_1(fn, a0) \
	ADD	$-24, X2; \
	MOV	a0, 8(X2); \
	CALL	fn(SB); \
	MOV	16(X2), T0; \
	ADD	$24, X2

// GO_CALL_2_1(fn, a0, a1) - 2 args, 1 return in T0
// Frame: 32 bytes (8 reserved + 16 args + 8 return)
#define GO_CALL_2_1(fn, a0, a1) \
	ADD	$-32, X2; \
	MOV	a0, 8(X2); \
	MOV	a1, 16(X2); \
	CALL	fn(SB); \
	MOV	24(X2), T0; \
	ADD	$32, X2

// GO_CALL_4_0(fn, a0, a1, a2, a3) - 4 args, 0 returns
// Frame: 72 bytes (8 reserved + 32 args + 32 return space for callee safety)
// Note: some 4-arg functions (e.g. TimerIRQHandler) have return values in their
// Go signature even though the caller ignores them. We allocate the full 72
// bytes to prevent the callee's .abi0 wrapper from writing past our frame.
#define GO_CALL_4_0(fn, a0, a1, a2, a3) \
	ADD	$-72, X2; \
	MOV	a0, 8(X2); \
	MOV	a1, 16(X2); \
	MOV	a2, 24(X2); \
	MOV	a3, 32(X2); \
	CALL	fn(SB); \
	ADD	$72, X2

// GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) - 7 args, 1 return in T0
// Frame: 72 bytes (8 reserved + 56 args + 8 return)
#define GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) \
	ADD	$-72, X2; \
	MOV	a0, 8(X2); \
	MOV	a1, 16(X2); \
	MOV	a2, 24(X2); \
	MOV	a3, 32(X2); \
	MOV	a4, 40(X2); \
	MOV	a5, 48(X2); \
	MOV	a6, 56(X2); \
	CALL	fn(SB); \
	MOV	64(X2), T0; \
	ADD	$72, X2
