// go_abi_macros_arm64.h - Go ABIInternal calling convention macros for ARM64 assembly
//
// These macros simplify calling Go functions from assembly using the ABIInternal
// calling convention (register-based). On ARM64, Go uses R0-R15 for integer args
// and R0-R15 for returns.
//
// Usage: GO_CALL_N_M(function, arg_regs...)
//   N = number of arguments
//   M = number of returns (0 or 1)
//   arg_regs = registers containing the arguments (mapped to R0..RN-1)
//   Return value (if M=1) is in R0 after the call.
//
// IMPORTANT: These macros clobber R0-R17 (caller-saved registers per Go ABI).
// Preserve any needed values in callee-saved registers (R19-R28) before calling.

// GO_CALL_0_1(fn) - 0 args, 1 return in R0
#define GO_CALL_0_1(fn) \
	CALL fn(SB)

// GO_CALL_1_0(fn, a0) - 1 arg, 0 returns
#define GO_CALL_1_0(fn, a0) \
	MOVD a0, R0; \
	CALL fn(SB)

// GO_CALL_1_1(fn, a0) - 1 arg, 1 return in R0
#define GO_CALL_1_1(fn, a0) \
	MOVD a0, R0; \
	CALL fn(SB)

// GO_CALL_2_1(fn, a0, a1) - 2 args, 1 return in R0
#define GO_CALL_2_1(fn, a0, a1) \
	MOVD a1, R1; \
	MOVD a0, R0; \
	CALL fn(SB)

// GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) - 7 args, 1 return in R0
#define GO_CALL_7_1(fn, a0, a1, a2, a3, a4, a5, a6) \
	MOVD a6, R6; \
	MOVD a5, R5; \
	MOVD a4, R4; \
	MOVD a3, R3; \
	MOVD a2, R2; \
	MOVD a1, R1; \
	MOVD a0, R0; \
	CALL fn(SB)
