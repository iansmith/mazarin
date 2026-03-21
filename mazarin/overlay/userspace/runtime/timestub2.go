// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// MAZZY USERSPACE OVERLAY: Added !(linux && arm64) exclusion.
// On linux/arm64, walltime is implemented in Go (walltime_mazzy.go)
// instead of assembly, so this forward declaration must be excluded.

//go:build !aix && !darwin && !freebsd && !openbsd && !solaris && !wasip1 && !windows && !(linux && amd64) && !(linux && arm64) && !plan9

package runtime

//go:wasmimport gojs runtime.walltime
func walltime() (sec int64, nsec int32)
