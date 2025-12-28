.section ".text"

// get_caller_stack_pointer() - Returns the caller's stack pointer
// Uses frame pointer to compute caller's SP: FP + 16 (saved FP+LR) + 40 (frame offset)
// Returns: uintptr (64-bit) in x0
.global get_caller_stack_pointer
get_caller_stack_pointer:
    mov x0, x29        // Get caller's frame pointer
    add x0, x0, #56    // Caller's SP = FP + 56 (16 for saved FP+LR + 40 frame offset)
    ret
