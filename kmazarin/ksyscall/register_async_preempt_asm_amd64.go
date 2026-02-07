//go:build amd64

package ksyscall

import _ "unsafe"

// RegisterAsyncPreemptAddr registers the asyncPreempt address for the current priest.
// Implemented in main package.
//
//go:linkname RegisterAsyncPreemptAddr main.RegisterAsyncPreemptAddr
func RegisterAsyncPreemptAddr(asyncPreemptAddr uint64) int64
