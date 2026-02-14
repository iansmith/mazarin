// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// KMAZARIN OVERLAY: No-op checkfds for bare-metal kernel.
// The original checks that file descriptors 0/1/2 are open and opens /dev/null
// if not. Kmazarin has no filesystem, so we skip this entirely.

//go:build unix

package runtime

func checkfds() {
	// No-op: kmazarin has no /dev/null or file descriptor table.
}
