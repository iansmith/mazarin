// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cpu implements processor feature detection for bare-metal kernel.
package cpu

// ARM64 contains ARM64-specific CPU feature flags.
// For bare-metal kernel, we don't do runtime CPU detection.
// We set HasATOMICS=false to use the LDAXR/STLXR fallback path
// which is compatible with all ARMv8.0+ processors.
var ARM64 struct {
	_ CacheLinePad
	HasATOMICS bool // ARMv8.1 LSE atomics (SWPAL, CASAL, etc.)
	_ CacheLinePad
}

// CacheLinePad is used to pad structs to avoid false sharing.
type CacheLinePad struct{ _ [64]byte }

func init() {
	// For bare-metal: Use LDAXR/STLXR fallback (compatible with all ARM64).
	// If you want to use LSE atomics, set HasATOMICS = true here
	// (requires ARMv8.1+ CPU).
	ARM64.HasATOMICS = false
}
