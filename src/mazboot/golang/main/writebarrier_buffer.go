package main

import "unsafe"

// writeBarrierDummyBuffer is used by the write barrier stubs in writebarrier.s
//
// Mazboot runs without a full Go runtime - no GC, no P structures, no wbBuf.
// The Go compiler generates calls to gcWriteBarrier2/3/4 for pointer writes.
// These functions normally write to p.wbBuf for GC tracking, then return
// a pointer to that buffer so the caller can store pointers there.
//
// Since we don't have GC, our stubs return this dummy buffer address.
// The compiler-generated code writes to this buffer (harmless discard)
// AND to the actual destination (the real pointer assignment).
//
// Size: 128 uintptrs = 1024 bytes on arm64 (8 bytes each)
// This is enough for gcWriteBarrier4 which writes up to 32 bytes (4 pointers).
//
//go:notinheap
var writeBarrierDummyBuffer [128]uintptr

// Prevent the compiler from optimizing away writeBarrierDummyBuffer.
// This is referenced by assembly code in writebarrier.s.
//
//go:nosplit
func writeBarrierDummyBufferKeepAlive() uintptr {
	return uintptr(unsafe.Pointer(&writeBarrierDummyBuffer[0]))
}
