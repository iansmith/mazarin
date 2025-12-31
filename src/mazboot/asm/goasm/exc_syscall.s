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
	// CRITICAL: Disable timer interrupts during exception handling
	MRS DAIF, R10
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

	// Set up arguments for SyncExceptionDispatch:
	// R0 = ec, R1 = esr, R2 = elr, R3 = far, R4 = spsr
	// R5 = syscallNum, R6 = arg0, R7 = arg1, stack: arg2-arg5, framePtr, savedFP, savedLR, savedG
	MOVD R10, R0                   // R0 = ec
	MOVD R15, R1                   // R1 = esr
	MOVD R12, R2                   // R2 = elr
	MOVD R14, R3                   // R3 = far
	MOVD R13, R4                   // R4 = spsr
	MOVD R23, R5                   // R5 = syscallNum (x8)
	MOVD R16, R6                   // R6 = arg0 (x0)
	MOVD R17, R7                   // R7 = arg1 (x1)

	// Remaining args go on stack (Go calling convention)
	// arg2 (R19), arg3 (R20), arg4 (R21), arg5 (R22), framePtr, savedFP, savedLR, savedG
	SUB $128, RSP                  // Allocate stack space for args + callee-saved
	MOVD R19, 64(RSP)              // arg2
	MOVD R20, 72(RSP)              // arg3
	MOVD R21, 80(RSP)              // arg4
	MOVD R22, 88(RSP)              // arg5
	ADD $128, RSP, R9              // R9 = frame pointer (SP before this allocation)
	MOVD R9, 96(RSP)               // framePtr
	MOVD R24, 104(RSP)             // savedFP
	MOVD R25, 112(RSP)             // savedLR
	MOVD R26, 120(RSP)             // savedG

	// Save callee-saved registers we used
	STP (R19, R20), 0(RSP)
	STP (R21, R22), 16(RSP)
	STP (R23, R24), 32(RSP)
	STP (R25, R26), 48(RSP)

	// Call the unified Go dispatcher
	CALL main·SyncExceptionDispatch(SB)
	// Returns: R0 = result, R1 = switchTo, R2 = handled

	// Restore callee-saved registers
	LDP 0(RSP), (R19, R20)
	LDP 16(RSP), (R21, R22)
	LDP 32(RSP), (R23, R24)
	LDP 48(RSP), (R25, R26)
	ADD $128, RSP                  // Deallocate args space

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

	// Call DoContextSwitch(framePtr, targetIdx)
	MOVD RSP, R0                   // R0 = frame pointer
	// R1 already has targetIdx

	SUB $64, RSP
	STP (R19, R20), 0(RSP)
	STP (R21, R22), 16(RSP)
	STP (R29, R30), 32(RSP)

	CALL main·DoContextSwitch(SB)
	// Returns: R0 = pointer to new ThreadContext

	LDP 0(RSP), (R19, R20)
	LDP 16(RSP), (R21, R22)
	LDP 32(RSP), (R29, R30)
	ADD $64, RSP

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

// ============================================================================
// LEGACY: handle_svc_syscall - Kept for backward compatibility
// ============================================================================
// This is called from the old GCC path. It now just jumps to sync_exception_entry.
// Eventually this can be removed once GCC assembly is fully updated.
//
TEXT handle_svc_syscall(SB), NOSPLIT|NOFRAME, $0
	B sync_exception_entry(SB)
