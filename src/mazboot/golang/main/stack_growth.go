//go:build qemuvirt && aarch64

package main

import (
	"unsafe"

	"mazboot/asm"
)

// Simplified stack growth for bare-metal kernel
// This implements a minimal version of Go's stack growth that:
// 1. Uses our heap allocator (kmalloc/kfree) instead of OS calls
// 2. Doesn't aggressively shrink stacks (keeps them for reuse)
// 3. Works with compiler-generated stack checks
// 4. Supports future goroutines (each can have its own stack)

// Stack constants (matching Go runtime)
const (
	_StackMin   = 2048 // Minimum stack size (2KB)
	_StackGuard = 928  // Guard space before stack overflow
	_StackSmall = 128  // Small stack threshold

	// g0 stack bounds (system goroutine, 64KB - matches real Go runtime)
	// g0 stack: 0x5EFF0000 - 0x5F000000 (64KB, fixed size)
	G0_STACK_SIZE   = 64 * 1024 // 64KB (matches runtime/asm_arm64.s)
	G0_STACK_TOP    = 0x5F000000
	G0_STACK_BOTTOM = 0x5EFF0000 // G0_STACK_TOP - G0_STACK_SIZE

	// Main goroutine stack size (allocated from heap)
	KERNEL_GOROUTINE_STACK_SIZE = 64 * 1024 // 64KB (increased from 32KB due to deep call chains)

	// Legacy constants (deprecated, for compatibility)
	KERNEL_STACK_TOP    = G0_STACK_TOP
	KERNEL_STACK_BOTTOM = G0_STACK_BOTTOM
	KERNEL_STACK_SIZE   = G0_STACK_SIZE
)

// Stack growth constants
const (
	// Initial stack size when growing from pre-allocated region
	INITIAL_STACK_SIZE = 2 * 1024 * 1024 // 2MB initial stack
	// Minimum stack size when growing
	MIN_STACK_SIZE = 2048 // 2KB minimum
	// Stack growth factor (double each time)
	STACK_GROWTH_FACTOR = 2
)

// Stack structure tracks a goroutine's stack
// For now, we only have one (kernel), but this supports multiple goroutines
type stack struct {
	lo     uintptr // Low address (bottom, where stack can grow to)
	hi     uintptr // High address (top, initial/current stack pointer)
	size   uintptr // Current stack size
	guard0 uintptr // Stack guard (lo + _StackGuard)
	prev   *stack  // Previous stack (for stack chain, if we implement shrinking)
}

// Global kernel stack (for main kernel execution)
// Future: Each goroutine will have its own stack
var kernelStack stack

// Stack list (for tracking all stacks, future goroutine support)
var allStacks *stack

// initKernelStack initializes g0's stack (system goroutine)
// The stack pointer is set in boot.s to 0x5F000000 (g0 stack top)
// g0 uses a fixed 8KB stack at the top of kernel RAM
// Main kernel goroutine will have its own 32KB stack allocated from heap
//
//go:nosplit
func initKernelStack() {
	// g0 stack: fixed 64KB at 0x5EFF0000 - 0x5F000000
	// This is the system goroutine stack (for runtime operations)
	kernelStack.lo = G0_STACK_BOTTOM
	kernelStack.hi = G0_STACK_TOP
	kernelStack.size = G0_STACK_SIZE // g0 stack is fixed 64KB
	kernelStack.guard0 = G0_STACK_BOTTOM + _StackGuard
	kernelStack.prev = nil

	// Add to stack list (for future goroutine support)
	allStacks = &kernelStack
}

// getCurrentStack returns the current stack (for the executing goroutine)
// For now, always returns kernelStack since we're single-threaded
//
//go:nosplit
func getCurrentStack() *stack {
	// Future: Get stack from goroutine structure (g.stack)
	// For now, always return kernel stack
	return &kernelStack
}

// growStack allocates a new larger stack and copies the old one
// This is called by morestack() when stack check fails
// NOTE: This function must be very careful because it's called while
// executing on the old stack. We need to copy everything before updating SP.
//
//go:nosplit
func growStack(oldStack *stack) bool {
	// Calculate new stack size (double the old one, or use initial size if first growth)
	// If oldStack.size is 0 (initial), use INITIAL_STACK_SIZE
	var newSize uintptr
	if oldStack.size == 0 {
		newSize = INITIAL_STACK_SIZE
	} else {
		newSize = oldStack.size * STACK_GROWTH_FACTOR
	}
	if newSize < MIN_STACK_SIZE {
		newSize = MIN_STACK_SIZE
	}

	// Allocate new stack from heap
	newStackMem := kmalloc(uint32(newSize))
	if newStackMem == nil {
		print("Stack: alloc failed\r\n")
		return false
	}

	newStackBase := pointerToUintptr(newStackMem)
	newStackTop := newStackBase + newSize

	// Calculate how much of old stack is used
	// Stack grows downward, so used portion is from currentSP to oldStack.hi
	currentSP := asm.GetStackPointer()

	// For initial stack (size == 0), we use the pre-allocated region
	// Calculate used size based on how far SP has moved from initial position
	var usedSize uintptr
	if oldStack.size == 0 {
		// Initial stack: used size is from currentSP to initial top
		if currentSP > oldStack.hi {
			kfree(newStackMem)
			return false
		}
		usedSize = oldStack.hi - currentSP
	} else {
		// Grown stack: used size is from currentSP to oldStack.hi
		if currentSP > oldStack.hi {
			kfree(newStackMem)
			return false
		}
		usedSize = oldStack.hi - currentSP
	}

	// Copy used portion from old stack to new stack
	// Old stack: [lo ... currentSP ... hi] (grows downward)
	// New stack: [newBase ... newSP ... newTop] (grows downward)
	// We copy from currentSP to oldStack.hi (the used portion)
	// CRITICAL: newSP must be 16-byte aligned (AArch64 calling convention requirement)
	// Round down to 16-byte boundary to ensure alignment
	newSP := (newStackTop - usedSize) &^ 0xF

	// SP ALIGNMENT CHECK: Verify newSP is aligned before setting
	if (newSP & 0xF) != 0 {
		kfree(newStackMem)
		return false
	}

	// Copy memory (must be done before updating SP!)
	oldStackPtr := unsafe.Pointer(currentSP)
	newStackPtr := unsafe.Pointer(newSP)
	memmove(newStackPtr, oldStackPtr, uint32(usedSize))

	// Update stack structure
	oldStack.prev = oldStack // Save pointer to old stack for potential cleanup
	oldStack.lo = newStackBase
	oldStack.hi = newStackTop
	oldStack.size = newSize
	oldStack.guard0 = newStackBase + _StackGuard

	// Update stack pointer register
	// CRITICAL: After this, we're executing on the new stack!
	set_stack_pointer(newSP)

	// Memory barrier to ensure SP update is visible
	asm.Dsb()

	// Note: We don't free the old stack immediately
	// We keep it for potential reuse or cleanup later
	// This is simpler than Go's aggressive shrinking

	return true
}

// memmove copies memory from src to dst
// Simple byte-by-byte copy (can be optimized later)
//
//go:nosplit
func memmove(dst, src unsafe.Pointer, size uint32) {
	dstPtr := (*byte)(dst)
	srcPtr := (*byte)(src)

	// Handle overlapping regions
	if pointerToUintptr(dst) < pointerToUintptr(src) {
		// Copy forward
		for i := uint32(0); i < size; i++ {
			dstPtr = (*byte)(addToPointer(unsafe.Pointer(dst), uintptr(i)))
			srcPtr = (*byte)(addToPointer(unsafe.Pointer(src), uintptr(i)))
			*dstPtr = *srcPtr
		}
	} else {
		// Copy backward (for overlapping)
		for i := size; i > 0; i-- {
			dstPtr = (*byte)(addToPointer(unsafe.Pointer(dst), uintptr(i-1)))
			srcPtr = (*byte)(addToPointer(unsafe.Pointer(src), uintptr(i-1)))
			*dstPtr = *srcPtr
		}
	}

	// Memory barrier to ensure copy is visible
	asm.Dsb()
}

// set_stack_pointer updates the stack pointer register
// This must be implemented in assembly
//
//go:linkname set_stack_pointer set_stack_pointer
//go:nosplit
func set_stack_pointer(sp uintptr)

// GrowStackForCurrent is called from morestack assembly
// It gets the current stack and grows it
// This is a bridge function that can be called from assembly
// Must be exported (capitalized) so assembly can call it
// Marked as noinline and used in kernel.go to prevent optimization
//
//go:noinline
//export GrowStackForCurrent
func GrowStackForCurrent() {
	currentStack := getCurrentStack()
	if !growStack(currentStack) {
		// Stack growth failed - panic
		print("FATAL: stack growth failed\r\n")
		for {
		}
	}
}
