#include "textflag.h"

// get_caller_stack_pointer() - Returns the caller's stack pointer
// Uses frame pointer to compute caller's SP: FP + 16 (saved FP+LR) + 40 (frame offset)
// Returns: uintptr (64-bit) in R0
// func get_caller_stack_pointer() uintptr
TEXT ·get_caller_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
    MOVD R29, R0          // Get caller's frame pointer (x29 → R29)
    ADD  $56, R0          // Caller's SP = FP + 56 (16 for saved FP+LR + 40 frame offset)
    RET
