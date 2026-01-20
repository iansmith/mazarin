// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// KMAZARIN OVERLAY: Extends archauxv to parse custom auxv entries for heap bounds.
// This allows Cardinal to pass heap configuration to the runtime overlay
// BEFORE the first mmap call, enabling dynamic heap sizing.

//go:build arm64

package runtime

import "internal/cpu"

// Custom auxv tags for kmazarin (must match cardinal/constants/layout.go)
const (
	_AT_KMAZARIN_HEAP_START = 0x1010
	_AT_KMAZARIN_HEAP_END   = 0x1011
)

// Kernel heap bounds - set by archauxv before first mmap call.
// These are used by the cgo_mmap.go overlay for the bump allocator.
// Initial values of 0 indicate "not yet initialized from auxv".
var kmazarinHeapStart uintptr
var kmazarinHeapEnd uintptr

func archauxv(tag, val uintptr) {
	switch tag {
	case _AT_HWCAP:
		cpu.HWCap = uint(val)
	case _AT_KMAZARIN_HEAP_START:
		kmazarinHeapStart = val
	case _AT_KMAZARIN_HEAP_END:
		kmazarinHeapEnd = val
	}
}

func osArchInit() {}

//go:nosplit
func cputicks() int64 {
	// nanotime() is a poor approximation of CPU ticks that is enough for the profiler.
	return nanotime()
}
