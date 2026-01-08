// exc_syscall.s - Synchronous exception handling in Go/Plan9 assembly
//
// This file provides the unified synchronous exception handling path:
//   1. sync_exception_entry - Entry point from GCC sync_exception_handler
//   2. syscall_return       - Restores registers and executes ERET (for syscalls)
//   3. exception_return     - Restores ALL registers and executes ERET (for page faults)
//   4. load_context_and_eret - Context switch to new thread
//
// Exception frame layout (320 bytes, saved by sync_exception_handler):
//   [SP+0]:   x0, x1        (16 bytes)
//   [SP+16]:  x2, x3        (16 bytes)
//   [SP+32]:  x4, x5        (16 bytes)
//   [SP+48]:  x6, x7        (16 bytes)
//   [SP+64]:  x8, x9        (16 bytes)
//   [SP+80]:  x10, x11      (16 bytes)
//   [SP+96]:  x12, x13      (16 bytes)
//   [SP+112]: x14, x15      (16 bytes)
//   [SP+128]: x16, x17      (16 bytes)
//   [SP+144]: x18, x19      (16 bytes)
//   [SP+160]: x20, x21      (16 bytes)
//   [SP+176]: x22, x23      (16 bytes)
//   [SP+192]: x24, x25      (16 bytes)
//   [SP+208]: x26, x27      (16 bytes)
//   [SP+224]: x28           (8 bytes)
//   [SP+232]: x29, x30      (16 bytes)
//   [SP+248]: original SP   (8 bytes)
//   [SP+256]: ELR_EL1, SPSR_EL1 (16 bytes)
//   [SP+272]: FAR_EL1, ESR_EL1  (16 bytes)
//   [SP+288]: SP_EL0        (8 bytes)
//   [SP+296]: saved g (for syscall) (8 bytes)
//   [SP+304]: saved x0 (for syscall) (8 bytes)
//   [SP+312]: saved LR (for syscall) (8 bytes)

#include "textflag.h"
#include "../../../../../docs/abi/go_abi_macros_arm64.h"

// Exception frame offsets
#define EXC_FRAME_X0         0
#define EXC_FRAME_X8         64
#define EXC_FRAME_X28        224
#define EXC_FRAME_X29_X30    232
#define EXC_FRAME_ELR_SPSR   256
#define EXC_FRAME_FAR_ESR    272
#define EXC_FRAME_SP_EL0     288
#define EXC_FRAME_SAVED_G    296
#define EXC_FRAME_SAVED_X0   304
#define EXC_FRAME_SAVED_LR   312

// ============================================================================
// sync_exception_entry - Unified entry point for ALL synchronous exceptions
// ============================================================================
// Called from GCC sync_exception_handler after exception frame is set up.
//
// Entry state:
//   - SP: Points to exception frame (320 bytes) on exception stack
//   - Exception frame contains all saved registers + system state
//   - We are in EL1h mode (using SP_EL1)
//
// This function:
//   1. Disables timer interrupts
//   2. Switches to g0 for Go runtime safety
//   3. Extracts EC from ESR to determine exception type
//   4. Calls SyncExceptionDispatch with all necessary parameters
//   5. Based on return: syscall_return, exception_return, context_switch, or hang
//
TEXT sync_exception_entry(SB), NOSPLIT|NOFRAME, $0
	// CRITICAL: Disable IRQ interrupts during exception handling
	//
	// DAIF bits:
	//   D (bit 9, 0x200) = Debug exceptions mask
	//   A (bit 8, 0x100) = SError (asynchronous abort) mask
	//   I (bit 7, 0x80)  = IRQ mask
	//   F (bit 6, 0x40)  = FIQ mask
	//
	// We only disable IRQ (I bit = 0x80) which is sufficient for preventing
	// timer interrupts from nesting during exception handling.
	//
	// NOTE: Debug exceptions (D) and SError (A) are very unlikely to be the
	// source of nested exception problems. Masking them would be overkill:
	//   - Debug exceptions (D): Only occur from breakpoints/watchpoints/single-step
	//     which we don't use during normal operation
	//   - SError (A): Asynchronous system errors (like ECC memory errors) are
	//     rare hardware faults, not software-triggered
	//
	// FIQ (F) could be masked for extra safety, but we don't currently use FIQ.
	//
	MRS DAIF, R10

	// DEBUG: Check if IRQs were ALREADY disabled before we disable them
	// This helps detect if interrupt enable/disable is unbalanced
	TST $0x80, R10                 // Test I bit (bit 7)
	BEQ irqs_were_enabled
	// IRQs were already disabled - this is unexpected, print 'd'
	MOVD $0x09000000, R11
	MOVD $'d', R12
	MOVB R12, (R11)
irqs_were_enabled:

	ORR $0x80, R10, R10            // Set I bit to disable IRQ
	MSR R10, DAIF
	ISB $15

	// Save original g to exception frame before switching to g0
	MOVD g, EXC_FRAME_SAVED_G(RSP)

	// Switch to g0 for Go runtime safety
	MOVD $runtime·g0(SB), g

	// Load exception info from frame and system registers
	// ESR is at offset 280 (272 + 8), ELR at 256, SPSR at 264, FAR at 272
	LDP EXC_FRAME_FAR_ESR(RSP), (R14, R15)   // R14 = FAR, R15 = ESR
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)  // R12 = ELR, R13 = SPSR

	// Extract EC from ESR (bits 31:26)
	LSR $26, R15, R10              // R10 = EC (exception class)
	AND $0x3F, R10, R10            // Mask to 6 bits

	// Load syscall arguments from exception frame (needed if EC=0x15)
	LDP EXC_FRAME_X0(RSP), (R16, R17)        // R16 = x0 (arg0), R17 = x1 (arg1)
	LDP 16(RSP), (R19, R20)                  // R19 = x2 (arg2), R20 = x3 (arg3)
	LDP 32(RSP), (R21, R22)                  // R21 = x4 (arg4), R22 = x5 (arg5)
	MOVD EXC_FRAME_X8(RSP), R23              // R23 = x8 (syscall number)

	// Load saved FP, LR, g for traceback
	LDP EXC_FRAME_X29_X30(RSP), (R24, R25)   // R24 = saved FP, R25 = saved LR
	MOVD EXC_FRAME_X28(RSP), R26             // R26 = saved g

	// NEW APPROACH: Build ExceptionContext struct and use GO_CALL_8_2B macro
	//
	// Current register contents:
	//   R10 = EC, R15 = ESR, R12 = ELR, R14 = FAR, R13 = SPSR
	//   R16 = arg0 (x0), R17 = arg1 (x1)
	//   R19 = arg2, R20 = arg3, R21 = arg4, R22 = arg5
	//   R23 = syscall number, R24 = savedFP, R25 = savedLR, R26 = savedG
	//
	// Function signature:
	//   func SyncExceptionDispatch(
	//       ctx *ExceptionContext,
	//       syscallNum uint64,
	//       arg0, arg1, arg2, arg3, arg4, arg5 uint64,
	//   ) (result int64, switchTo int32, handled bool)
	//
	// ExceptionContext layout (72 bytes):
	//   [0]:  EC       [8]:  ESR      [16]: ELR      [24]: FAR
	//   [32]: SPSR     [40]: FramePtr [48]: SavedFP  [56]: SavedLR
	//   [64]: SavedG

	// Allocate space for ExceptionContext (72 bytes, round to 80)
	SUB $80, RSP

	// Build ExceptionContext at RSP+0..71
	MOVD R10, 0(RSP)               // ctx.EC
	MOVD R15, 8(RSP)               // ctx.ESR
	MOVD R12, 16(RSP)              // ctx.ELR
	MOVD R14, 24(RSP)              // ctx.FAR
	MOVD R13, 32(RSP)              // ctx.SPSR
	ADD $80, RSP, R0               // FramePtr = original RSP (exception frame)
	MOVD R0, 40(RSP)               // ctx.FramePtr
	MOVD R24, 48(RSP)              // ctx.SavedFP
	MOVD R25, 56(RSP)              // ctx.SavedLR
	MOVD R26, 64(RSP)              // ctx.SavedG

	// Prepare arguments in registers for GO_CALL_8_2B
	MOVD RSP, R0                   // arg0 = &ctx
	MOVD R23, R1                   // arg1 = syscallNum
	MOVD R16, R2                   // arg2 = arg0
	MOVD R17, R3                   // arg3 = arg1
	MOVD R19, R4                   // arg4 = arg2
	MOVD R20, R5                   // arg5 = arg3
	MOVD R21, R6                   // arg6 = arg4
	MOVD R22, R7                   // arg7 = arg5

	// Call using macro (allocates its own 96-byte frame)
	GO_CALL_8_2B(main·SyncExceptionDispatch, R0, R1, R2, R3, R4, R5, R6, R7, R0, R1, R2)
	// Returns: R0 = result (int64), R1 = switchTo (int32), R2 = handled (bool)

	// Deallocate frame
	ADD $80, RSP

	// Check if handled
	CBZ R2, do_exception_hang      // If not handled, hang

	// Check if context switch needed
	CMP $0, R1
	BGE do_context_switch

	// Check if this was a syscall (need to preserve R0 as return value)
	// If switchTo == -1 and we came from syscall, use syscall_return
	// Otherwise use exception_return (restores all registers)
	//
	// We need to check the EC from the exception frame to know which return to use
	// Load ESR again and check EC
	MOVD EXC_FRAME_FAR_ESR+8(RSP), R10    // R10 = ESR (offset 280 = 272 + 8)
	LSR $26, R10, R10
	AND $0x3F, R10, R10
	CMP $0x15, R10                 // Was it SVC (syscall)?
	BEQ do_syscall_return

	// Not a syscall - use exception_return (restores ALL registers including R0)
	B exception_return(SB)

do_syscall_return:
	// Syscall - R0 has return value, use syscall_return
	B syscall_return(SB)

do_context_switch:
	// R0 = result (syscall return value to save)
	// R1 = target thread index
	MOVD R0, R19                   // Save result in callee-saved
	MOVD R1, R20                   // Save targetIdx in callee-saved
	MOVD RSP, R21                  // Save framePtr (current RSP = exception frame)

	// Save callee-saved registers
	SUB $48, RSP
	STP (R19, R20), 0(RSP)
	STP (R21, R22), 16(RSP)
	STP (R29, R30), 32(RSP)

	// Call DoContextSwitch using macro
	// func DoContextSwitch(framePtr uintptr, targetIdx int32) *ThreadContext
	// Note: R20 is int32, but Go ABI0 promotes to 64-bit on stack
	MOVD R21, R0                   // arg0 = framePtr
	MOVD R20, R1                   // arg1 = targetIdx (promoted to uint64)
	GO_CALL_2_1(main·DoContextSwitch, R0, R1)
	// Returns: R0 = pointer to new ThreadContext

	// Restore callee-saved registers
	LDP 0(RSP), (R19, R20)
	LDP 16(RSP), (R21, R22)
	LDP 32(RSP), (R29, R30)
	ADD $48, RSP

	B load_context_and_eret(SB)

do_exception_hang:
	// Exception not handled - hang
	WFI
	B do_exception_hang

// ============================================================================
// syscall_return - Restore registers and return from syscall
// ============================================================================
// Entry state:
//   - R0: Syscall return value (DO NOT restore from frame!)
//   - SP: Points to exception frame (320 bytes)
//
// CRITICAL: R0 contains the syscall return value - DO NOT restore it!
//
TEXT syscall_return(SB), NOSPLIT|NOFRAME, $0
	// Restore ELR_EL1 and SPSR_EL1 from exception frame
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)
	MSR R12, ELR_EL1
	MSR R13, SPSR_EL1
	ISB $15

	// Restore SP_EL0
	MOVD EXC_FRAME_SP_EL0(RSP), R10
	MSR R10, SP_EL0
	ISB $15

	// Restore R1-R30 from exception frame (NOT R0!)
	LDP 16(RSP), (R2, R3)
	LDP 32(RSP), (R4, R5)
	LDP 48(RSP), (R6, R7)
	LDP 64(RSP), (R8, R9)
	LDP 80(RSP), (R10, R11)
	LDP 96(RSP), (R12, R13)
	LDP 112(RSP), (R14, R15)
	LDP 128(RSP), (R16, R17)
	// R18 is platform register - skip
	MOVD 152(RSP), R19
	LDP 160(RSP), (R20, R21)
	LDP 176(RSP), (R22, R23)
	LDP 192(RSP), (R24, R25)
	LDP 208(RSP), (R26, R27)
	MOVD EXC_FRAME_SAVED_G(RSP), g    // Restore original g
	LDP EXC_FRAME_X29_X30(RSP), (R29, R30)
	MOVD 8(RSP), R1                // Restore R1 LAST

	ADD $320, RSP                  // Deallocate exception frame
	ERET

// ============================================================================
// exception_return - Restore ALL registers and return from exception
// ============================================================================
// Entry state:
//   - SP: Points to exception frame (320 bytes)
//
// Used for page faults where we need to retry the faulting instruction
// with the EXACT same register state.
//
TEXT exception_return(SB), NOSPLIT|NOFRAME, $0
	// Restore ELR_EL1 and SPSR_EL1 from exception frame
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)
	MSR R12, ELR_EL1
	MSR R13, SPSR_EL1
	ISB $15

	// Restore SP_EL0
	MOVD EXC_FRAME_SP_EL0(RSP), R10
	MSR R10, SP_EL0
	ISB $15

	// Restore ALL registers including R0
	LDP 0(RSP), (R0, R1)           // Restore R0, R1
	LDP 16(RSP), (R2, R3)
	LDP 32(RSP), (R4, R5)
	LDP 48(RSP), (R6, R7)
	LDP 64(RSP), (R8, R9)
	LDP 80(RSP), (R10, R11)
	LDP 96(RSP), (R12, R13)
	LDP 112(RSP), (R14, R15)
	LDP 128(RSP), (R16, R17)
	// R18 is platform register - skip
	MOVD 152(RSP), R19
	LDP 160(RSP), (R20, R21)
	LDP 176(RSP), (R22, R23)
	LDP 192(RSP), (R24, R25)
	LDP 208(RSP), (R26, R27)
	MOVD EXC_FRAME_X28(RSP), g     // Restore g from exception frame
	LDP EXC_FRAME_X29_X30(RSP), (R29, R30)

	ADD $320, RSP                  // Deallocate exception frame
	ERET

// ============================================================================
// load_context_and_eret - Load thread context and switch to new thread
// ============================================================================
// Entry: R0 = pointer to ThreadContext struct
//
TEXT load_context_and_eret(SB), NOSPLIT|NOFRAME, $0
	MOVD R0, R10                   // Save context pointer

	// Debug: print 'S' for switch
	MOVD $0x09000000, R11
	MOVD $'S', R12
	MOVW R12, (R11)

	// Load SP_EL0 from Context.SP (offset 248)
	MOVD 248(R10), R11
	MSR R11, SP_EL0
	ISB $15

	// Load ELR_EL1 from Context.ELR (offset 256)
	MOVD 256(R10), R11
	MSR R11, ELR_EL1
	ISB $15

	// Load SPSR_EL1 from Context.SPSR (offset 264)
	MOVD 264(R10), R11

	// DEBUG: Print 'i' if SPSR.I bit is set (IRQs will be disabled after ERET)
	TST $0x80, R11                 // Test bit 7 (I mask)
	BEQ spsr_irq_ok
	MOVD $0x09000000, R12
	MOVD $'i', R13
	MOVB R13, (R12)                // Print 'i' = IRQs disabled in SPSR
spsr_irq_ok:

	MSR R11, SPSR_EL1
	ISB $15

	// Load all GPRs from Context.X[]
	LDP 0(R10), (R0, R1)
	LDP 16(R10), (R2, R3)
	LDP 32(R10), (R4, R5)
	LDP 48(R10), (R6, R7)
	LDP 64(R10), (R8, R9)
	LDP 96(R10), (R12, R13)
	LDP 112(R10), (R14, R15)
	LDP 128(R10), (R16, R17)
	// R18 is platform register - skip
	MOVD 152(R10), R19
	LDP 160(R10), (R20, R21)
	LDP 176(R10), (R22, R23)
	LDP 192(R10), (R24, R25)
	LDP 208(R10), (R26, R27)
	MOVD 224(R10), g               // g (R28)
	LDP 232(R10), (R29, R30)

	// Load R10, R11 last
	MOVD 88(R10), R11
	MOVD 80(R10), R10              // Clobbers base pointer - MUST BE LAST

	ERET

