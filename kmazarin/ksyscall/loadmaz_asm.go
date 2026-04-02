package ksyscall

import _ "unsafe" // for go:linkname

// submitLoadMaz submits a LoadMaz request to the kernel worker goroutine.
// Returns the next thread's context pointer for SetSyscallSwitchTarget,
// or 0 on failure (busy or no ready thread).
//
//go:linkname submitLoadMaz main.SubmitLoadMaz
func submitLoadMaz(req LoadMazWorkRequest) uintptr
