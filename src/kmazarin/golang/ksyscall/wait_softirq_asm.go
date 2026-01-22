//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for soft IRQ functions provided via go:linkname.

// ThreadBlockSoftIRQ blocks current thread waiting for soft IRQ.
// Links to wrapper in main package that provides SchedulerFunc.
//
//go:linkname ThreadBlockSoftIRQ main.threadBlockSoftIRQWrapper
func ThreadBlockSoftIRQ(bundlePtr uint64) uintptr

// RegisterSoftIRQDispatcher registers current thread as the soft IRQ dispatcher.
//
//go:linkname RegisterSoftIRQDispatcher main.RegisterSoftIRQDispatcher
func RegisterSoftIRQDispatcher() int64

// GetPendingSoftIRQ drains one item from the overflow queue.
//
//go:linkname GetPendingSoftIRQ main.GetPendingSoftIRQ
func GetPendingSoftIRQ(bundlePtr uint64) bool
