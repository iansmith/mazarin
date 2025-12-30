// lib_runtime.s - Go runtime initialization wrapper functions in Go/Plan9 assembly
//
// This file contains functions that call Go runtime initialization functions
// (runtime.args, runtime.osinit, runtime.schedinit, runtime.newproc, runtime.mstart)
// and the kernel_main bridge function.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.
//
// Migrated from asm/aarch64/lib.s

#include "textflag.h"

// External Go runtime symbols
// Note: In Go assembly, we reference these with package prefix or via linkname
// The Go linker resolves these at link time

// ============================================================================
// Runtime Initialization Wrappers
// ============================================================================

// call_runtime_args() int
// Sets up minimal Linux-style argv/envp/auxv structure and calls runtime.args
// Returns 0 on success (args completed without crash)
//
// NOTE: Only provides AT_PAGESZ for now (AT_RANDOM requires VirtIO RNG init)
TEXT call_runtime_args(SB), NOSPLIT, $96-4
	// Build the argv/envp/auxv structure on stack
	// Layout (each entry 8 bytes):
	//   SP+48: argv[0] = NULL (end of argv, argc=0)
	//   SP+56: envp[0] = NULL (end of envp)
	//   SP+64: AT_PAGESZ (6)
	//   SP+72: 4096
	//   SP+80: AT_NULL (0)
	//   SP+88: 0

	// argv[0] = NULL (end of argv)
	MOVD	ZR, 48(RSP)
	// envp[0] = NULL (end of envp)
	MOVD	ZR, 56(RSP)
	// auxv[0] = AT_PAGESZ (6), auxv[1] = 4096
	MOVD	$6, R0
	MOVD	R0, 64(RSP)
	MOVD	$4096, R0
	MOVD	R0, 72(RSP)
	// auxv[2] = AT_NULL (0), auxv[3] = 0
	MOVD	ZR, 80(RSP)
	MOVD	ZR, 88(RSP)

	// Call runtime.args(argc=0, argv=&sp[48])
	MOVW	ZR, R0			// argc = 0 (int32)
	ADD	$48, RSP, R1		// argv = pointer to our structure

	CALL	runtime·args(SB)

	// If we get here, args() completed without crash
	MOVW	ZR, R0			// Return 0 = success
	RET

// call_runtime_osinit() int
// Calls runtime.osinit() to test syscalls:
//   - sched_getaffinity (for getCPUCount)
//   - openat (for getHugePageSize - should fail gracefully)
// Returns 0 on success (osinit completed without crash)
TEXT call_runtime_osinit(SB), NOSPLIT, $32-4
	// Call runtime.osinit()
	// This will call getCPUCount() which uses sched_getaffinity syscall
	// and getHugePageSize() which tries to open /sys/... (will fail gracefully)
	CALL	runtime·osinit(SB)

	// If we get here, osinit() completed without crash
	MOVW	ZR, R0			// Return 0 = success
	RET

// call_runtime_schedinit() int
// Call runtime.schedinit() to initialize scheduler and locks
// Returns 0 on success
TEXT call_runtime_schedinit(SB), NOSPLIT, $32-4
	// Call runtime.schedinit()
	// This will:
	// - Call lockInit() for all runtime locks (uses futex syscall)
	// - Initialize scheduler structures
	// - Set up processors (P)
	// - Initialize system monitor
	CALL	runtime·schedinit(SB)

	// If we get here, schedinit() completed without crash
	MOVW	ZR, R0			// Return 0 = success
	RET

// call_runtime_newproc() int
// Calls runtime.newproc(runtime.mainPC) to create the main goroutine
// Returns 0 on success (newproc completed without crash)
//
// runtime.newproc takes a *funcval as parameter.
// funcval is a struct with a single field: fn uintptr (function pointer)
// runtime.mainPC is a global variable (funcval) containing the address of runtime.main
//
// ARM64 calling convention for newproc:
//   SP+0: dummy LR (0)
//   SP+8: pointer to funcval (address of runtime.mainPC)
TEXT call_runtime_newproc(SB), NOSPLIT, $48-4
	// Load address of runtime.mainPC (this is a *funcval)
	MOVD	$runtime·mainPC(SB), R0

	// Set up stack for newproc call:
	//   SP+0: dummy LR (0)
	//   SP+8: funcval pointer (runtime.mainPC)
	SUB	$16, RSP
	MOVD	ZR, (RSP)		// Store 0 at SP+0 (dummy LR)
	MOVD	R0, 8(RSP)		// Store funcval pointer at SP+8

	// Call runtime.newproc
	CALL	runtime·newproc(SB)

	// Clean up newproc's stack frame
	ADD	$16, RSP

	// If we get here, newproc() completed without crash
	MOVW	ZR, R0			// Return 0 = success
	RET

// call_newproc_simple_main() int
// Call runtime.newproc(simpleMainPC) to create goroutine for our simple test
// simpleMainPC is a funcval (pointer to main.simpleMain)
// Returns 0 on success (newproc completed without crash)
TEXT call_newproc_simple_main(SB), NOSPLIT, $48-4
	// Load address of simpleMainPC (the funcval structure)
	// This is like runtime.mainPC - it contains a pointer to the function
	MOVD	$simpleMainPC(SB), R0

	// Set up stack for newproc call:
	//   SP+0: dummy LR (0)
	//   SP+8: funcval pointer (simpleMainPC)
	SUB	$16, RSP
	MOVD	ZR, (RSP)		// Store 0 at SP+0 (dummy LR)
	MOVD	R0, 8(RSP)		// Store funcval pointer at SP+8

	// Call runtime.newproc
	CALL	runtime·newproc(SB)

	// Clean up newproc's stack frame
	ADD	$16, RSP

	// If we get here, newproc() completed without crash
	MOVW	ZR, R0			// Return 0 = success
	RET

// call_runtime_mstart()
// Call runtime.mstart() to start the scheduler
// This function should never return
TEXT call_runtime_mstart(SB), NOSPLIT, $32-0
	// Call runtime.mstart.abi0()
	// This should never return - it starts the scheduler
	// Note: The symbol is runtime.mstart.abi0 (with dots, not underscores)
	CALL	runtime·mstart·abi0(SB)

	// If we somehow get here (should never happen), just return
	RET

// ============================================================================
// Kernel Main Bridge
// ============================================================================

// kernel_main is called from boot.s after basic setup
// It bridges to main.KernelMain (Go function)
// Arguments from boot.s:
//   R0 = 0 (reserved)
//   R1 = 0 (reserved)
//   R2 = DTB pointer (passed by QEMU)
TEXT kernel_main(SB), NOSPLIT, $16-0
	// UART will be initialized by uartInit() called from kernel_main
	// No early debug writes

	// Function signature: KernelMain(r0, r1, atags uint32)
	// AArch64 calling convention: first 8 parameters in R0-R7
	//
	// NOTE: In QEMU virt, the DTB pointer is provided by QEMU in R0 at reset.
	// boot.s preserves that pointer and passes it to kernel_main in R2.
	// Keep R2 intact, set R0 and R1 to 0
	MOVD	ZR, R0			// r0 = 0
	MOVD	ZR, R1			// r1 = 0

	// Set R28 (goroutine pointer) to point to runtime.g0
	// This is required for write barrier to work
	MOVD	$runtime·g0(SB), g	// g is alias for R28

	// Note: Write barrier flag is set in boot.s AFTER BSS clear
	// (Setting it here would be overwritten by BSS clear)

	// Call Go function - this will initialize everything
	CALL	main·KernelMain(SB)

	// KernelMain returns after initialization is complete
	RET

// ============================================================================
// Stack Growth Functions (Bare-Metal Implementation)
// ============================================================================
// These functions are called by the Go compiler when a function
// needs more stack space. For our large pre-allocated stack,
// these should never be called. If they are, it indicates a stack overflow.

// runtime.morestack is called by Go compiler when stack check fails
// This implements simplified stack growth for bare-metal
TEXT runtime·morestack(SB), NOSPLIT|NOFRAME, $0-0
	// Save link register and frame pointer
	SUB	$96, RSP
	STP	(R29, R30), (RSP)
	MOVD	RSP, R29		// Set frame pointer

	// Save callee-saved registers (R19-R26, then R27 separately)
	// Note: g (R28) is managed by runtime, not saved here
	STP	(R19, R20), 16(RSP)
	STP	(R21, R22), 32(RSP)
	STP	(R23, R24), 48(RSP)
	STP	(R25, R26), 64(RSP)
	MOVD	R27, 80(RSP)		// R27 saved separately

	// Print 'S' to show we're in morestack (stack overflow)
	MOVD	$0x09000000, R1
	MOVD	$'S', R0
	MOVB	R0, (R1)

	// TODO: Implement stack growth
	// For now, just halt if morestack is called (shouldn't happen with large pre-allocated stack)
halt_morestack:
	B	halt_morestack

	// Restore callee-saved registers (unreachable, but kept for completeness)
	MOVD	80(RSP), R27		// R27 restored separately
	LDP	64(RSP), (R25, R26)
	LDP	48(RSP), (R23, R24)
	LDP	32(RSP), (R21, R22)
	LDP	16(RSP), (R19, R20)
	LDP	(RSP), (R29, R30)
	ADD	$96, RSP

	RET

// runtime.morestack_noctxt is called for functions without context
TEXT runtime·morestack_noctxt(SB), NOSPLIT|NOFRAME, $0-0
	JMP	runtime·morestack(SB)

// NOTE: runtime.morestackc is provided by Go runtime (don't redefine)

// ============================================================================
// Jump to Kmazarin Kernel
// ============================================================================

// jump_to_kmazarin(entryAddr uintptr, argc uint64, argv uintptr, stackPointer uintptr)
// This function sets up the Go runtime environment and jumps to kmazarin
// Parameters (Go 1.17+ register ABI):
//   R0 = entryAddr - address to jump to
//   R1 = argc - argument count
//   R2 = argv - pointer to argv array
//   R3 = stackPointer - pointer to argc/argv/envp/auxv structure
// Sets up registers as expected by Go runtime _rt0_arm64_linux:
//   R0 = argc
//   R1 = argv
//   SP = stackPointer (pointing to the full structure)
// NOTE: This function never returns
TEXT jump_to_kmazarin(SB), NOSPLIT|NOFRAME, $0-32
	// Save entry point address to R4 (we need R0 for argc)
	MOVD	R0, R4

	// DEBUG: Print DAIF value before jumping
	// Save all caller args to stack
	SUB	$32, RSP
	STP	(R0, R1), (RSP)
	STP	(R2, R3), 16(RSP)

	// Print "D="
	MOVD	$0x09000000, R10
	MOVD	$'D', R0
	MOVB	R0, (R10)
	MOVD	$'=', R0
	MOVB	R0, (R10)

	// Read DAIF and print as hex
	MRS	DAIF, R5
	// Print high nibble (bits 15-12)
	LSR	$12, R5, R0
	AND	$0xF, R0
	CMP	$10, R0
	BLT	daif_digit1
	ADD	$('A'-10), R0
	B	daif_print1
daif_digit1:
	ADD	$'0', R0
daif_print1:
	MOVB	R0, (R10)
	// Print next nibble (bits 11-8)
	LSR	$8, R5, R0
	AND	$0xF, R0
	CMP	$10, R0
	BLT	daif_digit2
	ADD	$('A'-10), R0
	B	daif_print2
daif_digit2:
	ADD	$'0', R0
daif_print2:
	MOVB	R0, (R10)
	// Print next nibble (bits 7-4)
	LSR	$4, R5, R0
	AND	$0xF, R0
	CMP	$10, R0
	BLT	daif_digit3
	ADD	$('A'-10), R0
	B	daif_print3
daif_digit3:
	ADD	$'0', R0
daif_print3:
	MOVB	R0, (R10)
	// Print low nibble (bits 3-0)
	AND	$0xF, R5, R0
	CMP	$10, R0
	BLT	daif_digit4
	ADD	$('A'-10), R0
	B	daif_print4
daif_digit4:
	ADD	$'0', R0
daif_print4:
	MOVB	R0, (R10)

	// Print newline
	MOVD	$'\r', R0
	MOVB	R0, (R10)
	MOVD	$'\n', R0
	MOVB	R0, (R10)

	// Restore args
	LDP	16(RSP), (R2, R3)
	LDP	(RSP), (R0, R1)
	ADD	$32, RSP

	// Restore entry point from R4
	MOVD	R4, R4			// Keep entry point in R4

	// Set up Go runtime registers:
	// R0 = argc (from R1)
	MOVD	R1, R0

	// R1 = argv (from R2)
	MOVD	R2, R1

	// SP = stackPointer (from R3)
	// CRITICAL: The Go runtime expects SP to point to the start of the structure
	// which contains argc at [SP+0], argv at [SP+8], envp at [SP+16], auxv at [SP+32]
	MOVD	R3, RSP

	// Jump to kmazarin entry point (_rt0_arm64_linux)
	// At this point:
	//   R0 = argc = 1
	//   R1 = argv = pointer to argv array
	//   SP = pointer to full argc/argv/envp/auxv structure
	JMP	(R4)			// Branch to entry point - never returns

// ============================================================================
// Data: simpleMainPC funcval
// ============================================================================

// simpleMainPC is a funcval structure pointing to main.simpleMain
// This is similar to runtime.mainPC - a funcval pointing to our main function
DATA	simpleMainPC(SB)/8, $main·simpleMain(SB)
GLOBL	simpleMainPC(SB), RODATA, $8
