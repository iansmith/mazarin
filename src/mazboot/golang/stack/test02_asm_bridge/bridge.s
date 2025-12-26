// Assembly bridge function that demonstrates Go ↔ Assembly interface
//
// Function signature:
//   func AsmBridge(a int64, b int64, msg string) (result int64, tag string)
//
// Parameters (on entry):
//   R0 = a (int64)
//   R1 = b (int64)
//   R2 = msg.ptr (string pointer)
//   R3 = msg.len (string length)
//
// Return values (on exit):
//   R0 = result (int64)
//   R1 = tag.ptr (string pointer)
//   R2 = tag.len (string length)

#include "textflag.h"

// AsmBridge receives parameters from Go, calls GoHelper, returns results
TEXT ·AsmBridge(SB), NOSPLIT, $64-56
    // Frame layout (64 bytes):
    //   [SP+56] = return: tag.len (8 bytes)
    //   [SP+48] = return: tag.ptr (8 bytes)
    //   [SP+40] = return: result (8 bytes)
    //   [SP+32] = param: msg.len (8 bytes)
    //   [SP+24] = param: msg.ptr (8 bytes)
    //   [SP+16] = param: b (8 bytes)
    //   [SP+8]  = param: a (8 bytes)
    //   [SP+0]  = (locals/scratch space)

    // Parameters are already in FP, not SP, because this is not NOSPLIT...
    // Wait, we ARE NOSPLIT, so let me reconsider...
    //
    // With NOSPLIT, parameters are accessed via FP (frame pointer)
    // The frame size $64 is for local variables only
    // Parameters: a+0(FP), b+8(FP), msg_ptr+16(FP), msg_len+24(FP)
    // Returns: result+32(FP), tag_ptr+40(FP), tag_len+48(FP)

    // Save parameters to local stack so we can use registers freely
    MOVD    R0, 8(RSP)      // Save a
    MOVD    R1, 16(RSP)     // Save b
    MOVD    R2, 24(RSP)     // Save msg.ptr
    MOVD    R3, 32(RSP)     // Save msg.len

    // Do some arithmetic: multiply a by 2
    MOVD    R0, R4
    ADD     R4, R4, R4      // R4 = a * 2

    // Prepare to call GoHelper(a*2, b*3, msg, "from-asm")
    // GoHelper signature: func(x int64, y int64, s1 string, s2 string) (sum int64, combined string)

    // Calculate b * 3
    MOVD    R1, R5
    ADD     R1, R5, R5
    ADD     R1, R5, R5      // R5 = b * 3

    // Set up parameters for GoHelper:
    // R0 = x (a*2)
    // R1 = y (b*3)
    // R2 = s1.ptr (msg.ptr)
    // R3 = s1.len (msg.len)
    // R4 = s2.ptr ("from-asm")
    // R5 = s2.len (8)

    MOVD    R4, R0          // x = a*2
    MOVD    R5, R1          // y = b*3
    MOVD    24(RSP), R2     // s1.ptr = msg.ptr
    MOVD    32(RSP), R3     // s1.len = msg.len

    // Load address of "from-asm" string constant
    MOVD    $·asmstring(SB), R4     // s2.ptr
    MOVD    $8, R5                  // s2.len = 8

    // Call GoHelper
    CALL    ·GoHelper(SB)

    // GoHelper returns:
    // R0 = sum (int64)
    // R1 = combined.ptr (string pointer)
    // R2 = combined.len (string length)

    // Save return values from GoHelper
    MOVD    R0, 40(RSP)     // Save sum
    MOVD    R1, 48(RSP)     // Save combined.ptr
    MOVD    R2, 56(RSP)     // Save combined.len

    // Prepare return values for our caller
    // result = sum + original a
    MOVD    8(RSP), R3      // Load original a
    ADD     R3, R0, R0      // result = sum + a

    // tag = combined (already in R1, R2)

    // Return values are now in:
    // R0 = result
    // R1 = tag.ptr
    // R2 = tag.len

    RET

// String constant for "from-asm"
DATA    ·asmstring+0(SB)/8, $"from-asm"
GLOBL   ·asmstring(SB), RODATA, $8
