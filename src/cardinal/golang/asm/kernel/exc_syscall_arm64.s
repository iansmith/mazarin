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

	// CALLING CONVENTION FIX: SyncExceptionDispatch is ABI0
	// The .abi0 wrapper does: str x30, [sp, #-16]!
	// Then loads args from [SP+24], [SP+32], ... [SP+144]
	// After wrapper prologue: wrapper_SP = our_SP - 16
	// So wrapper reads from [our_SP + 8], [our_SP + 16], ... [our_SP + 128]
	// Returns are stored at [our_SP + 136], [our_SP + 144], [our_SP + 148]
	//
	// Current register contents (sources):
	//   R10 = EC, R15 = ESR, R12 = ELR, R14 = FAR, R13 = SPSR
	//   R16 = arg0 (x0), R17 = arg1 (x1)
	//   R19 = arg2, R20 = arg3, R21 = arg4, R22 = arg5
	//   R23 = syscall number, R24 = savedFP, R25 = savedLR, R26 = savedG
	//
	// Stack layout after SUB $272, RSP:
	// Go's generated .abi0 wrapper does: str x30, [sp, #-16]!
	// Then loads args from [wrapper_SP+24], [wrapper_SP+32], etc.
	// After wrapper prologue: wrapper_SP = our_SP - 16
	// So wrapper loads from: our_SP + 8, our_SP + 16, ... our_SP + 136
	// Returns go to: our_SP + 144, our_SP + 152, our_SP + 156
	//
	//   [RSP+8]:   arg0 (ec) -> wrapper reads from SP+24 = RSP+8
	//   [RSP+16]:  arg1 (esr)
	//   ...
	//   [RSP+128]: arg15 (savedG)
	//   [RSP+136]: return0 (result)
	//   [RSP+144]: return1 (switchTo)
	//   [RSP+148]: return2 (handled)
	//   [RSP+152]: saved R19-R26 (64 bytes)
	//   [RSP+216]: temp storage (48 bytes)

	// Allocate space
	SUB $272, RSP

	// Save callee-saved registers
	STP (R19, R20), 152(RSP)
	STP (R21, R22), 168(RSP)
	STP (R23, R24), 184(RSP)
	STP (R25, R26), 200(RSP)

	// Save conflicting sources to temp area
	MOVD R10, 216(RSP)             // Save EC
	MOVD R15, 224(RSP)             // Save ESR
	MOVD R12, 232(RSP)             // Save ELR
	MOVD R14, 240(RSP)             // Save FAR
	MOVD R13, 248(RSP)             // Save SPSR

	// Store args to stack for Go's .abi0 wrapper (args start at RSP+8)
	// Order: ec, esr, elr, far, spsr, syscallNum, arg0..5, framePtr, savedFP, savedLR, savedG
	MOVD 216(RSP), R0              // Load EC from temp
	MOVD R0, 8(RSP)                // arg0 = ec
	MOVD 224(RSP), R0              // Load ESR from temp
	MOVD R0, 16(RSP)               // arg1 = esr
	MOVD 232(RSP), R0              // Load ELR from temp
	MOVD R0, 24(RSP)               // arg2 = elr
	MOVD 240(RSP), R0              // Load FAR from temp
	MOVD R0, 32(RSP)               // arg3 = far
	MOVD 248(RSP), R0              // Load SPSR from temp
	MOVD R0, 40(RSP)               // arg4 = spsr
	MOVD R23, 48(RSP)              // arg5 = syscallNum
	MOVD R16, 56(RSP)              // arg6 = arg0
	MOVD R17, 64(RSP)              // arg7 = arg1
	MOVD R19, 72(RSP)              // arg8 = arg2
	MOVD R20, 80(RSP)              // arg9 = arg3
	MOVD R21, 88(RSP)              // arg10 = arg4
	MOVD R22, 96(RSP)              // arg11 = arg5
	ADD $272, RSP, R0              // framePtr = original RSP
	MOVD R0, 104(RSP)              // arg12 = framePtr
	MOVD R24, 112(RSP)             // arg13 = savedFP
	MOVD R25, 120(RSP)             // arg14 = savedLR
	MOVD R26, 128(RSP)             // arg15 = savedG

	// Call the Go exception dispatcher
	// SyncExceptionDispatch is a manual stub in abi_stubs_arm64.s that:
	// 1. Loads args from stack (ABI0 style)
	// 2. Calls syncExceptionDispatchInternal via BL (direct to ABIInternal)
	// This avoids nested wrapper issues.
	CALL main·SyncExceptionDispatch(SB)

	// Load return values from stack
	// The stub stores returns at: result at caller_SP+136, switchTo at SP+144, handled at SP+148
	MOVD 136(RSP), R0              // result
	MOVW 144(RSP), R1              // switchTo
	MOVBU 148(RSP), R2             // handled

	// Restore callee-saved registers
	LDP 152(RSP), (R19, R20)
	LDP 168(RSP), (R21, R22)
	LDP 184(RSP), (R23, R24)
	LDP 200(RSP), (R25, R26)
	ADD $272, RSP                  // Deallocate space

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

	// DoContextSwitch(framePtr, targetIdx) - ABI0 expects args on stack
	// Layout: [RSP+0..7]=pad, [RSP+8..15]=framePtr, [RSP+16..23]=targetIdx,
	//         [RSP+24..31]=return, [RSP+32..79]=callee-saved
	SUB $80, RSP

	// Store args on stack (ABI0)
	MOVD R21, 8(RSP)               // arg0 = framePtr
	MOVW R20, 16(RSP)              // arg1 = targetIdx (int32)

	// Save callee-saved registers
	STP (R19, R20), 32(RSP)
	STP (R21, R22), 48(RSP)
	STP (R29, R30), 64(RSP)

	CALL main·DoContextSwitch(SB)
	// Returns: R0 = pointer to new ThreadContext (also at RSP+24)

	// Restore callee-saved registers
	LDP 32(RSP), (R19, R20)
	LDP 48(RSP), (R21, R22)
	LDP 64(RSP), (R29, R30)
	ADD $80, RSP

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

