//go:build qemu

package main

import (
	"unsafe"
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

	// Kernel stack bounds (from linker.ld and boot.s)
	// Stack top: 0x60000000 (top of 512MB kernel region)
	// Stack bottom: 0x41000000 (16MB into RAM, leaves room for page array + heap)
	// Stack size: ~496MB (plenty for development)
	KERNEL_STACK_TOP    = 0x60000000
	KERNEL_STACK_BOTTOM = 0x41000000
	KERNEL_STACK_SIZE   = KERNEL_STACK_TOP - KERNEL_STACK_BOTTOM // ~496MB
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

// initKernelStack initializes the kernel's initial stack
// The stack pointer is set in boot.s to 0x60000000
// We track this as our initial stack
// Note: We use the large pre-allocated region, but set size to 0
// to indicate it's the initial stack (will use INITIAL_STACK_SIZE on first growth)
//
//go:nosplit
func initKernelStack() {
	// For initial kernel stack, we use the pre-allocated region
	// Stack grows downward from 0x60000000 to 0x40400000
	// But we mark size as 0 to indicate it's the initial pre-allocated stack
	// If stack growth is needed, we'll allocate from heap
	kernelStack.lo = KERNEL_STACK_BOTTOM
	kernelStack.hi = KERNEL_STACK_TOP
	kernelStack.size = 0 // 0 means using pre-allocated region (will grow to INITIAL_STACK_SIZE if needed)
	kernelStack.guard0 = KERNEL_STACK_BOTTOM + _StackGuard
	kernelStack.prev = nil

	// Add to stack list (for future goroutine support)
	allStacks = &kernelStack

	uartPuts("Stack OK\r\n")
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
		uartPuts("Stack: ERROR - Failed to allocate new stack from heap\r\n")
		return false
	}

	newStackBase := pointerToUintptr(newStackMem)
	newStackTop := newStackBase + newSize

	// Calculate how much of old stack is used
	// Stack grows downward, so used portion is from currentSP to oldStack.hi
	currentSP := get_stack_pointer()

	// For initial stack (size == 0), we use the pre-allocated region
	// Calculate used size based on how far SP has moved from initial position
	var usedSize uintptr
	if oldStack.size == 0 {
		// Initial stack: used size is from currentSP to initial top
		if currentSP > oldStack.hi {
			// SP is above stack top - shouldn't happen
			uartPuts("Stack: ERROR - Invalid stack pointer (above top)\r\n")
			kfree(newStackMem)
			return false
		}
		usedSize = oldStack.hi - currentSP
	} else {
		// Grown stack: used size is from currentSP to oldStack.hi
		if currentSP > oldStack.hi {
			uartPuts("Stack: ERROR - Invalid stack pointer\r\n")
			kfree(newStackMem)
			return false
		}
		usedSize = oldStack.hi - currentSP
	}

	// Copy used portion from old stack to new stack
	// Old stack: [lo ... currentSP ... hi] (grows downward)
	// New stack: [newBase ... newSP ... newTop] (grows downward)
	// We copy from currentSP to oldStack.hi (the used portion)
	newSP := newStackTop - usedSize

	// Copy memory (must be done before updating SP!)
	oldStackPtr := unsafe.Pointer(currentSP)
	newStackPtr := unsafe.Pointer(newSP)
	memmove(newStackPtr, oldStackPtr, uint32(usedSize))

	// Update stack structure (save old stack info first)
	var oldSize uintptr
	if oldStack.size == 0 {
		oldSize = KERNEL_STACK_SIZE // Initial pre-allocated size
	} else {
		oldSize = oldStack.size
	}
	oldStack.prev = oldStack // Save pointer to old stack for potential cleanup
	oldStack.lo = newStackBase
	oldStack.hi = newStackTop
	oldStack.size = newSize
	oldStack.guard0 = newStackBase + _StackGuard

	// Update stack pointer register
	// CRITICAL: After this, we're executing on the new stack!
	set_stack_pointer(newSP)

	// Memory barrier to ensure SP update is visible
	dsb()

	uartPuts("Stack: Grew from ")
	if oldSize >= 1024*1024 {
		uartPutUint32(uint32(oldSize / (1024 * 1024)))
		uartPuts("MB to ")
		uartPutUint32(uint32(newSize / (1024 * 1024)))
		uartPuts("MB\r\n")
	} else {
		uartPutUint32(uint32(oldSize / 1024))
		uartPuts("KB to ")
		uartPutUint32(uint32(newSize / 1024))
		uartPuts("KB\r\n")
	}

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
	dsb()
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
//go:nosplit
//go:noinline
//export GrowStackForCurrent
func GrowStackForCurrent() {
	currentStack := getCurrentStack()
	if !growStack(currentStack) {
		// Stack growth failed - panic
		uartPuts("Stack: CRITICAL - Stack growth failed, halting\r\n")
		for {
			// Infinite loop
		}
	}
}

