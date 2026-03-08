//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// BlockForBlockIO blocks the current thread waiting for block I/O completion.
// Re-checks ioComplete under scheduler lock to prevent missed-wakeup race.
//
//go:linkname BlockForBlockIO main.BlockForBlockIO
func BlockForBlockIO(ioComplete *uint32) uintptr
