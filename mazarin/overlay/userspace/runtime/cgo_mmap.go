// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Mazzy overlay: Routes runtime mmap through an interceptable function pointer.
// This allows syscall interception to work for heap allocation during runtime init.

//go:build (linux && (amd64 || arm64 || loong64)) || (freebsd && amd64)

package runtime

import "unsafe"

// _cgo_mmap is filled in by runtime/cgo when it is linked into the
// program, so it is only non-nil when using cgo.
//
//go:linkname _cgo_mmap _cgo_mmap
var _cgo_mmap unsafe.Pointer

// _cgo_munmap is filled in by runtime/cgo when it is linked into the
// program, so it is only non-nil when using cgo.
//
//go:linkname _cgo_munmap _cgo_munmap
var _cgo_munmap unsafe.Pointer

// MazzyMmapHandler is a function pointer for mmap interception.
// By default it's nil, which means use sysMmap (real SVC).
// The syscall package sets this to route mmap through PriestSyscallEntry.
// Exported via linkname without package prefix so syscall can set it.
//
//go:linkname MazzyMmapHandler MazzyMmapHandler
var MazzyMmapHandler func(addr uintptr, n uintptr, prot, flags, fd int32, off uint32) (uintptr, int)

// mmap is used to route the mmap system call through an interceptable path,
// allowing Mazzy's syscall interception to work for heap allocation.
//
//go:nosplit
func mmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (unsafe.Pointer, int) {
	if _cgo_mmap != nil {
		// CGO path - use the cgo mmap function
		var ret uintptr
		systemstack(func() {
			ret = callCgoMmap(addr, n, prot, flags, fd, off)
		})
		if ret < 4096 {
			return nil, int(ret)
		}
		return unsafe.Pointer(ret), 0
	}

	// Mazzy path: check if there's a custom handler
	if MazzyMmapHandler != nil {
		p, err := MazzyMmapHandler(uintptr(addr), n, prot, flags, fd, off)
		return unsafe.Pointer(p), err
	}

	// Default: use sysMmap (real SVC via assembly)
	return sysMmap(addr, n, prot, flags, fd, off)
}

// munmap routes through syscall package for consistency
//
//go:nosplit
func munmap(addr unsafe.Pointer, n uintptr) {
	if _cgo_munmap != nil {
		systemstack(func() { callCgoMunmap(addr, n) })
		return
	}
	sysMunmap(addr, n)
}

// sysMmap calls the mmap system call. It is implemented in assembly.
func sysMmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (p unsafe.Pointer, err int)

// callCgoMmap calls the mmap function in the runtime/cgo package
// using the GCC calling convention. It is implemented in assembly.
func callCgoMmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) uintptr

// sysMunmap calls the munmap system call. It is implemented in assembly.
func sysMunmap(addr unsafe.Pointer, n uintptr)

// callCgoMunmap calls the munmap function in the runtime/cgo package
// using the GCC calling convention. It is implemented in assembly.
func callCgoMunmap(addr unsafe.Pointer, n uintptr)
