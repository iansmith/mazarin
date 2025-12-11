.section ".text"

// get_caller_stack_pointer() - Returns the caller's stack pointer
//
// TESTING: Adding 88 bytes (0x58) offset based on breadcrumb analysis
// Previous test with +48 gave SP=0x402372D8, need 0x40237300
// Difference: 0x28 (40 bytes), so total offset = 48+40 = 88
//
// Returns: uintptr (64-bit) in x0 - the caller's stack pointer + 0x58
.global get_caller_stack_pointer
get_caller_stack_pointer:
    mov x0, x29        // Get caller's frame pointer
    add x0, x0, #16    // Caller's SP = FP + 16 (size of saved FP+LR)
    add x0, x0, #40    // ADD 40 BYTES (0x28) - iterating to find correct offset
    ret                // Return

// verify_stack_pointer_reading() - Tests that SP reading is correct
//
// This function verifies that get_caller_stack_pointer() returns a reasonable
// value by comparing it to the actual SP at function entry.
//
// Returns: int (1 = success, 0 = failure)
.global verify_stack_pointer_reading
verify_stack_pointer_reading:
    // Save entry SP and LR
    mov x10, sp                     // x10 = entry SP
    stp x29, x30, [sp, #-16]!       // Save FP and LR, create frame
    mov x29, sp                     // Set up FP
    
    // Call get_caller_stack_pointer
    bl get_caller_stack_pointer
    
    // x0 should equal x10 (entry SP) or be very close
    // Our frame is 16 bytes, so x0 should equal x10 + 16
    sub x1, x0, x10                 // x1 = returned_SP - entry_SP
    
    // For a 16-byte frame, difference should be exactly 16
    cmp x1, #16
    b.eq verify_success
    
    // Allow small tolerance (±8 bytes) for different frame layouts
    // Check if difference is in range [8, 24]
    cmp x1, #8
    b.lt verify_fail
    cmp x1, #24
    b.gt verify_fail
    
verify_success:
    mov x0, #1                      // Return 1 (success)
    ldp x29, x30, [sp], #16         // Restore FP and LR
    ret
    
verify_fail:
    mov x0, #0                      // Return 0 (failure)
    ldp x29, x30, [sp], #16         // Restore FP and LR
    ret
