// Put dummy buffer in BSS (will be cleared at boot)
// This is safe - it's in the normal BSS section after other globals
.section ".bss"
.align 4
.global write_barrier_dummy_buffer
write_barrier_dummy_buffer:
    .space 1024    // 1KB dummy buffer for write barrier (discarded writes)

.section ".text"

// Custom write barrier functions that perform the actual assignment
// This replaces the Go runtime's write barrier functions to work in bare-metal
//
// From disassembly analysis of the calling code:
//   - x27 = destination address (heapSegmentListHead) - set before call
//   - x2 = new value (pointer to assign)
//   - x1 = old value (loaded before call, for GC tracking - we don't need it)
//
// The original write barrier functions write to a buffer for GC tracking.
// Our version: Perform the actual assignment directly since we don't have GC.
//
// Note: The calling code also does "str x2, [x27]" after gcWriteBarrier returns,
// but we'll do it here to ensure it happens even if the calling code path changes.

// gcWriteBarrier2 - called for 2-pointer writes (16 bytes)
// This global symbol should override the Go runtime's local symbol
.global runtime.gcWriteBarrier2
runtime.gcWriteBarrier2:
    // Return a dummy buffer address in x25
    // The caller will write to the buffer ([x25], [x25+8], etc.)
    // AND to the actual destination ([x27+offset])
    // We use a dummy buffer so the buffer writes don't corrupt memory
    adrp x25, write_barrier_dummy_buffer
    add  x25, x25, :lo12:write_barrier_dummy_buffer
    ret

// gcWriteBarrier3 - called for 3-pointer writes (24 bytes)
.global runtime.gcWriteBarrier3
runtime.gcWriteBarrier3:
    // Return dummy buffer address
    adrp x25, write_barrier_dummy_buffer
    add  x25, x25, :lo12:write_barrier_dummy_buffer
    ret

// gcWriteBarrier4 - called for 4-pointer writes (32 bytes)
.global runtime.gcWriteBarrier4
runtime.gcWriteBarrier4:
    // Return dummy buffer address
    adrp x25, write_barrier_dummy_buffer
    add  x25, x25, :lo12:write_barrier_dummy_buffer
    ret

// Main gcWriteBarrier function (called by gcWriteBarrier2/3/4 with x25 = size)
// Also provide alias name that objcopy will redirect to
.global gcWriteBarrier
.global our_gcWriteBarrier
gcWriteBarrier:
our_gcWriteBarrier:
    // If we get here, it means one of the specific functions wasn't found
    // Just return - the calling code will do the assignment
    ret
