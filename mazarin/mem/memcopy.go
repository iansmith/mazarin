package mem

import "unsafe"

// MemCopy copies n bytes from src to dst using optimized architecture-specific
// assembly. After MemCopy returns, all written bytes are guaranteed visible to
// any consumer on the same coherency domain (inner-shareable on ARM64, system
// domain on x86_64 and RISC-V). This makes it safe to hand the destination
// buffer to another goroutine or shepherd immediately after the call.
//
// src and dst must not overlap. n must be non-negative; if n <= 0 no bytes
// are copied.
func MemCopy(dst, src unsafe.Pointer, n int64)
