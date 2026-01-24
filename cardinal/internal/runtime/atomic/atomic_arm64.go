// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build arm64

package atomic

import (
	"unsafe"
)

// For bare-metal kernel: We don't have CPU feature detection.
// The assembly code will use LDAXR/STLXR fallback (compatible with all ARM64).
// If we want to use ARMv8.1 LSE atomics, we can build with GOARM64_LSE=1.
const (
	offsetARM64HasATOMICS = 0 // Not used in bare-metal
)

//go:noescape
func Xadd(ptr *uint32, delta int32) uint32

//go:noescape
func Xadd64(ptr *uint64, delta int64) uint64

//go:noescape
func Xadduintptr(ptr *uintptr, delta uintptr) uintptr

//go:noescape
func Xchg8(ptr *uint8, new uint8) uint8

//go:noescape
func Xchg(ptr *uint32, new uint32) uint32

//go:noescape
func Xchg64(ptr *uint64, new uint64) uint64

//go:noescape
func Xchguintptr(ptr *uintptr, new uintptr) uintptr

//go:noescape
func Load(ptr *uint32) uint32

//go:noescape
func Load8(ptr *uint8) uint8

//go:noescape
func Load64(ptr *uint64) uint64

// NO go:noescape annotation; *ptr escapes if result escapes (#31525)
func Loadp(ptr unsafe.Pointer) unsafe.Pointer

//go:noescape
func LoadAcq(addr *uint32) uint32

//go:noescape
func LoadAcq64(ptr *uint64) uint64

//go:noescape
func LoadAcquintptr(ptr *uintptr) uintptr

//go:noescape
func Or8(ptr *uint8, val uint8)

//go:noescape
func And8(ptr *uint8, val uint8)

//go:noescape
func And(ptr *uint32, val uint32)

//go:noescape
func Or(ptr *uint32, val uint32)

//go:noescape
func And32(ptr *uint32, val uint32) uint32

//go:noescape
func Or32(ptr *uint32, val uint32) uint32

//go:noescape
func And64(ptr *uint64, val uint64) uint64

//go:noescape
func Or64(ptr *uint64, val uint64) uint64

//go:noescape
func Anduintptr(ptr *uintptr, val uintptr) uintptr

//go:noescape
func Oruintptr(ptr *uintptr, val uintptr) uintptr

//go:noescape
func Cas64(ptr *uint64, old, new uint64) bool

//go:noescape
func CasRel(ptr *uint32, old, new uint32) bool

//go:noescape
func Store(ptr *uint32, val uint32)

//go:noescape
func Store8(ptr *uint8, val uint8)

//go:noescape
func Store64(ptr *uint64, val uint64)

// NO go:noescape annotation; see atomic_pointer.go.
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer)

//go:noescape
func StoreRel(ptr *uint32, val uint32)

//go:noescape
func StoreRel64(ptr *uint64, val uint64)

//go:noescape
func StoreReluintptr(ptr *uintptr, val uintptr)

// Declarations for Cas and other functions
//go:noescape
func Cas(ptr *uint32, old, new uint32) bool

//go:noescape
func Casint32(ptr *int32, old, new int32) bool

//go:noescape
func Casint64(ptr *int64, old, new int64) bool

//go:noescape
func Casuintptr(ptr *uintptr, old, new uintptr) bool

//go:noescape
func Casp1(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool

//go:noescape
func Loaduintptr(ptr *uintptr) uintptr

//go:noescape
func Loaduint(ptr *uint) uint

//go:noescape
func Loadint32(ptr *int32) int32

//go:noescape
func Loadint64(ptr *int64) int64

//go:noescape
func Xaddint32(ptr *int32, delta int32) int32

//go:noescape
func Xaddint64(ptr *int64, delta int64) int64

//go:noescape
func Xchgint32(ptr *int32, new int32) int32

//go:noescape
func Xchgint64(ptr *int64, new int64) int64

//go:noescape
func Storeuintptr(ptr *uintptr, val uintptr)

//go:noescape
func Storeint32(ptr *int32, val int32)

//go:noescape
func Storeint64(ptr *int64, val int64)
