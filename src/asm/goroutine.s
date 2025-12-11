// Goroutine switching assembly functions
// Simplified versions of Go runtime's gogo() function

// switchToGoroutine switches execution to a new goroutine
// This is called from Go code on g0's stack (the system goroutine)
// This is the proper pattern: g0 switches to user goroutines
// Parameters:
//   x0: Pointer to new goroutine (runtimeG*)
//   x1: Function address to call (uintptr, currently unused - we call kernelMainBodyWrapper directly)
//
// This function:
//   1. Sets x28 (g pointer) to new goroutine
//   2. Sets SP to new goroutine's stack
//   3. Sets up call frame and calls kernelMainBodyWrapper
.global switchToGoroutine
.extern main.kernelMainBodyWrapper

switchToGoroutine:
    // x0 = new goroutine pointer (runtimeG*)
    // x1 = function address (currently unused)
    
    // Set x28 to new goroutine (CRITICAL for Go runtime)
    // The Go runtime reads the current goroutine via x28
    mov x28, x0
    
    // Get new goroutine's stack pointer from g.sched.sp
    // runtimeG layout: stack(16) + stackguard0(8) + stackguard1(8) + _panic(8) + _defer(8) + m(8) = 56
    // sched starts at offset 56
    // sched.sp is at offset 56 + 0 = 56
    ldr x2, [x0, #56]  // Load g.sched.sp
    
    // Set stack pointer to new goroutine's stack
    // The stack pointer should already be 16-byte aligned from goroutine creation
    mov sp, x2
    
    // Return to caller - they will call the Go function on the new stack
    // The return address is in lr (link register), not on the stack
    // So we can safely return even though we've switched stacks
    // The Go function will allocate its own frame when called
    ret
    
    // If kernelMainBodyWrapper returns (shouldn't happen), halt
halt_loop:
    wfe
    b halt_loop
