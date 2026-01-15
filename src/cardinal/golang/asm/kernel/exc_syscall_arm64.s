// exc_syscall_arm64.s - Synchronous Exception Dispatch and Return Paths
//
// ============================================================================
// OVERVIEW
// ============================================================================
// Provides the unified synchronous exception handling path for syscalls and
// page faults. Called after sync_exception_handler saves all registers.
//
// Functions:
//   - sync_exception_entry() - Dispatch to Go handler (7 segments)
//   - syscall_return() - Return from syscall with result (3 segments)
//   - exception_return() - Return from page fault (3 segments)
//   - load_context_and_eret() - Context switch to new thread (3 segments)
//
// OPERATION:
//   1. sync_exception_entry:
//      - Disables IRQs
//      - Switches to g0 stack (Go safety requirement)
//      - Extracts exception class (EC) from ESR
//      - Calls SyncExceptionDispatch (Go function)
//      - Routes to appropriate return path based on result
//
//   2. syscall_return: Returns R0 as result, preserves modified registers
//   3. exception_return: Restores ALL registers for retry
//   4. load_context_and_eret: Loads new thread context for context switch
//
// WHY NOT DECOMPOSE:
// Exception dispatch is a critical orchestration that cannot be decomposed:
//   1. IRQ masking must happen before any other operations
//   2. g0 stack switch required for Go runtime safety
//   3. Register restore must be atomic (no function calls)
//   4. ERET must happen immediately after restore
// Decomposing would break exception handling contract or introduce race conditions.
//
// ============================================================================
// SEGMENT OVERVIEW (see asm/docs/exception_handling_decomposition.md)
// ============================================================================
//
// sync_exception_entry segments:
//   Segment 1: IRQ Disable - Mask IRQ during exception handling
//   Segment 2: Save g & Switch to g0 - Preserve g, switch to g0 for Go safety
//   Segment 3: Load Exception Info - Read ESR, ELR, SPSR, FAR from frame
//   Segment 4: Extract EC & Load Args - Get exception class and syscall args
//   Segment 5: Build ExceptionContext - Create context struct on stack
//   Segment 6: Call Go Dispatcher - Call SyncExceptionDispatch
//   Segment 7: Handle Result - Route to appropriate return path
//
// syscall_return segments:
//   Segment 1: Restore System Regs - Restore ELR, SPSR, SP_EL0
//   Segment 2: Restore GPRs (except R0) - R0 has return value
//   Segment 3: ERET - Return from exception
//
// exception_return segments:
//   Segment 1: Restore System Regs - Restore ELR, SPSR, SP_EL0
//   Segment 2: Restore ALL GPRs - Including R0 for retry
//   Segment 3: ERET - Return from exception
//
// load_context_and_eret segments:
//   Segment 1: Load System Regs - Load SP_EL0, ELR, SPSR
//   Segment 2: Load GPRs - Load all registers from ThreadContext
//   Segment 3: ERET - Switch to new thread
//
// POST-BOOT NOTE: This is exception dispatch code - must remain inline assembly.
// The dispatcher calls Go functions but the entry/exit sequences are atomic.
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
	// ========================================================================
	// SEGMENT 1: IRQ Disable
	// ========================================================================
	// CRITICAL: Disable IRQ interrupts during exception handling.
	// DAIF bits: D(debug)=0x200, A(SError)=0x100, I(IRQ)=0x80, F(FIQ)=0x40
	// We only mask IRQ - sufficient for preventing timer interrupt nesting.
	MRS DAIF, R10
	ORR $0x80, R10, R10            // Set I bit to disable IRQ
	MSR R10, DAIF
	ISB $15

	// ========================================================================
	// SEGMENT 2: Save g & Switch to g0
	// ========================================================================
	// Save original g to exception frame before switching to g0 for Go safety.
	MOVD g, EXC_FRAME_SAVED_G(RSP)
	MOVD $runtime·g0(SB), g

	// ========================================================================
	// SEGMENT 3: Load Exception Info
	// ========================================================================
	// Load exception info from frame: ESR, ELR, SPSR, FAR
	LDP EXC_FRAME_FAR_ESR(RSP), (R14, R15)   // R14 = FAR, R15 = ESR
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)  // R12 = ELR, R13 = SPSR

	// ========================================================================
	// SEGMENT 4: Extract EC & Load Args
	// ========================================================================
	// Extract exception class from ESR (bits 31:26).
	// Load syscall arguments and saved registers for traceback.
	LSR $26, R15, R10              // R10 = EC (exception class)
	AND $0x3F, R10, R10            // Mask to 6 bits
	LDP EXC_FRAME_X0(RSP), (R16, R17)        // R16 = x0 (arg0), R17 = x1 (arg1)
	LDP 16(RSP), (R19, R20)                  // R19 = x2 (arg2), R20 = x3 (arg3)
	LDP 32(RSP), (R21, R22)                  // R21 = x4 (arg4), R22 = x5 (arg5)
	MOVD EXC_FRAME_X8(RSP), R23              // R23 = x8 (syscall number)
	LDP EXC_FRAME_X29_X30(RSP), (R24, R25)   // R24 = saved FP, R25 = saved LR
	MOVD EXC_FRAME_X28(RSP), R26             // R26 = saved g

	// ========================================================================
	// SEGMENT 5: Build ExceptionContext
	// ========================================================================
	// Build ExceptionContext struct on stack for Go dispatcher.
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

	// ========================================================================
	// SEGMENT 6: Call Go Dispatcher
	// ========================================================================
	// Prepare arguments and call SyncExceptionDispatch.
	MOVD RSP, R0                   // arg0 = &ctx
	MOVD R23, R1                   // arg1 = syscallNum
	MOVD R16, R2                   // arg2 = arg0
	MOVD R17, R3                   // arg3 = arg1
	MOVD R19, R4                   // arg4 = arg2
	MOVD R20, R5                   // arg5 = arg3
	MOVD R21, R6                   // arg6 = arg4
	MOVD R22, R7                   // arg7 = arg5
	GO_CALL_8_2B(main·SyncExceptionDispatch, R0, R1, R2, R3, R4, R5, R6, R7, R0, R1, R2)
	// Returns: R0 = result (int64), R1 = switchTo (int32), R2 = handled (bool)
	ADD $80, RSP                   // Deallocate ExceptionContext frame

	// ========================================================================
	// SEGMENT 7: Handle Result
	// ========================================================================
	// Route to appropriate return path based on dispatcher result.
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
	// ========================================================================
	// SEGMENT 1: Restore System Regs
	// ========================================================================
	// Restore ELR_EL1, SPSR_EL1, SP_EL0 from exception frame.
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)
	MSR R12, ELR_EL1
	MSR R13, SPSR_EL1
	ISB $15
	MOVD EXC_FRAME_SP_EL0(RSP), R10
	MSR R10, SP_EL0
	ISB $15

	// ========================================================================
	// SEGMENT 2: Restore GPRs (except R0)
	// ========================================================================
	// R0 contains syscall return value - DO NOT restore it!
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

	// ========================================================================
	// SEGMENT 3: ERET
	// ========================================================================
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
	// ========================================================================
	// SEGMENT 1: Restore System Regs
	// ========================================================================
	// Restore ELR_EL1, SPSR_EL1, SP_EL0 from exception frame.
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)
	MSR R12, ELR_EL1
	MSR R13, SPSR_EL1
	ISB $15
	MOVD EXC_FRAME_SP_EL0(RSP), R10
	MSR R10, SP_EL0
	ISB $15

	// ========================================================================
	// SEGMENT 2: Restore ALL GPRs
	// ========================================================================
	// Restore ALL registers including R0 for retry.
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

	// ========================================================================
	// SEGMENT 3: ERET
	// ========================================================================
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

	// ========================================================================
	// SEGMENT 1: Load System Regs
	// ========================================================================
	// Load SP_EL0, ELR_EL1, SPSR_EL1 from ThreadContext.
	MOVD 248(R10), R11             // Context.SP (offset 248)
	MSR R11, SP_EL0
	ISB $15
	MOVD 256(R10), R11             // Context.ELR (offset 256)
	MSR R11, ELR_EL1
	ISB $15
	MOVD 264(R10), R11             // Context.SPSR (offset 264)
	MSR R11, SPSR_EL1
	ISB $15

	// ========================================================================
	// SEGMENT 2: Load GPRs
	// ========================================================================
	// Load all general-purpose registers from ThreadContext.X[].
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

	// ========================================================================
	// SEGMENT 3: ERET
	// ========================================================================
	// Load R10, R11 last, then execute ERET to switch to new thread.
	MOVD 88(R10), R11
	MOVD 80(R10), R10              // Clobbers base pointer - MUST BE LAST
	ERET

