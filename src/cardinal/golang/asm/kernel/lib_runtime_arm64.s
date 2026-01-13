// lib_runtime.s - Go Runtime Initialization Wrappers (Go/Plan9 Assembly)
//
// This file contains functions that call Go runtime initialization functions
// (runtime.args, runtime.osinit, runtime.schedinit, runtime.newproc, runtime.mstart)
// and the kernel_main bridge function.
//
// ============================================================================
// SEGMENT OVERVIEW (see asm/docs/lib_runtime_decomposition.md)
// ============================================================================
//
// Runtime initialization wrappers:
//   call_runtime_args     - Build argv/envp/auxv, call runtime.args
//   call_runtime_osinit   - Call runtime.osinit()
//   call_runtime_schedinit - Call runtime.schedinit()
//   call_runtime_newproc  - Create main goroutine (runtime.mainPC)
//   call_newproc_simple_main - Create goroutine for simple test
//   call_runtime_mstart   - Start scheduler (never returns)
//
// Kernel entry points:
//   kernel_main           - Bridge from boot to main.KernelMain
//   jump_to_kmazarin      - Set up kmazarin environment and jump
//
// POST-BOOT NOTE: These wrappers are called during boot/initialization.
// They use ABI0 calling convention to interface with Go runtime.
// After boot completes, use standard Go function calls.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.
//

#include "textflag.h"
#include "../../../../../docs/abi/go_abi_macros_arm64.h"

// ============================================================================
// Runtime Initialization Wrappers
// ============================================================================

// call_runtime_args() int
// Sets up minimal Linux-style argv/envp/auxv structure and calls runtime.args
// Returns 0 on success (args completed without crash)
//
// NOTE: Only provides AT_PAGESZ for now (AT_RANDOM requires VirtIO RNG init)
//
// CALLING CONVENTION: We must follow the C/ABI0 calling convention here because
// that's what the Go program (kmazarin) expects from its "OS" (us). The Go runtime
// was compiled expecting Linux, which uses the standard ARM64 C ABI. When we call
// runtime.args, the .abi0 wrapper expects stack-based arguments:
//   - argc (int32) at [SP+8] before the call
//   - argv (**byte) at [SP+16] before the call
// The .abi0 wrapper then loads these into R0/R1 for the ABIInternal runtime.args
TEXT call_runtime_args(SB), NOSPLIT, $96-4
	// ========================================================================
	// SEGMENT 1: Build argv/envp/auxv Structure
	// ========================================================================
	// Build minimal Linux-style structure on stack.
	MOVD	ZR, 48(RSP)		// argv[0] = NULL (end of argv, argc=0)
	MOVD	ZR, 56(RSP)		// envp[0] = NULL (end of envp)
	MOVD	$6, R0			// AT_PAGESZ = 6
	MOVD	R0, 64(RSP)
	MOVD	$4096, R0
	MOVD	R0, 72(RSP)		// Page size = 4096
	MOVD	ZR, 80(RSP)		// AT_NULL = 0
	MOVD	ZR, 88(RSP)

	// ========================================================================
	// SEGMENT 2: Call runtime.args
	// ========================================================================
	// Set up ABI0 stack-based arguments.
	MOVW	ZR, 8(RSP)		// argc = 0
	ADD	$48, RSP, R0
	MOVD	R0, 16(RSP)		// argv pointer
	CALL	runtime·args(SB)

	// If we get here, args() completed without crash
	// Return 0 = success
	// NOTE: Go's ABIInternal wrapper expects return value on the stack.
	// For a function with $96-4 frame (compiled to 112-byte .abi0 frame),
	// the wrapper loads return value from [wrapper_SP+8] after call.
	// From our perspective before our epilogue, that's at [RSP+112+8] = [RSP+120].
	// But actually, the way Go's ABI works, the return space is at the start
	// of our frame, so we store at the return value offset in the frame header.
	// The wrapper's frame was 32 bytes; it loads from [SP+8] after we return.
	// Since our prologue did str x30, [sp, #-112]!, the caller's SP is at our RSP+112.
	// So return value goes at RSP+112+8 = RSP+120 from our perspective.
	MOVW	ZR, R0			// R0 = 0 for success
	MOVW	R0, 120(RSP)		// Store return value where wrapper expects it
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
TEXT kernel_main(SB), NOSPLIT, $0-0
	// UART will be initialized by uartInit() called from kernel_main
	// No early debug writes

	// Function signature: KernelMain(r0, r1, atags uint32)
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

	// Call Go function using Linux entry convention
	// This properly stores args to stack for ABI0, mimicking how
	// Go's rt0 receives argc/argv from Linux
	LINUX_ENTRY_CALL_3_0(main·KernelMain, R0, R1, R2)

	// KernelMain returns after initialization is complete
	RET

// ============================================================================
// Stack Growth Functions (Bare-Metal Implementation)
// ============================================================================
// NOTE: runtime.morestack and runtime.morestack_noctxt are now provided by Go runtime
// The old custom implementations have been removed to avoid duplicate symbol errors
// when using Go's native build toolchain.
//
// For bare-metal, the boot code sets up g0 with a large pre-allocated stack (64KB),
// and if morestack is ever called, Go's runtime will handle it (though we expect
// our stack should be sufficient for most kernel operations).

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
	// ========================================================================
	// SEGMENT 1: Save Entry Point
	// ========================================================================
	MOVD	R0, R4			// Save entry point (need R0 for argc)

	// ========================================================================
	// SEGMENT 2: Set Exception Vector
	// ========================================================================
	// Set VBAR_EL1 to kmazarin's exception vector.
	MOVD	main·LinkerKmazarinExceptionVector(SB), R6
	MSR	R6, VBAR_EL1
	DSB	$15
	ISB	$15

	// ========================================================================
	// SEGMENT 3: Switch Mode & Stack
	// ========================================================================
	// Switch to EL1t mode, set SP to kmazarin's g0 stack.
	MSR	$0, SPSel
	DSB	$15
	ISB	$15
	MOVD	R3, RSP			// SP = stackPointer arg

	// ========================================================================
	// SEGMENT 4: Set g & Jump
	// ========================================================================
	// Set g to kmazarin's runtime.g0, set up argc/argv, jump.
	MOVD	main·LinkerKmazarinRuntimeG0(SB), R5
	WORD	$0xAA0503FC		// mov x28, x5 (g = kmazarin g0)
	MOVD	R1, R0			// R0 = argc
	MOVD	R2, R1			// R1 = argv
	JMP	(R4)			// Branch to entry point - never returns

// ============================================================================
// Data: simpleMainPC funcval
// ============================================================================

// simpleMainPC is a funcval structure pointing to main.simpleMain
// This is similar to runtime.mainPC - a funcval pointing to our main function
DATA	simpleMainPC(SB)/8, $main·simpleMain(SB)
GLOBL	simpleMainPC(SB), RODATA, $8
