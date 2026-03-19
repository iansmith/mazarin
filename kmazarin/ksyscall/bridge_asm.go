package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // for go:linkname
)

// Forward declarations for bridge functions provided via go:linkname.
// These connect ksyscall to the scheduler in kmazarin/kmazarin.

// getCurrentThreadSIDAndTID returns the PID and TID of the current thread.
//
//go:linkname getCurrentThreadSIDAndTID main.GetCurrentThreadPIDAndTID
func getCurrentThreadSIDAndTID() (proc.ShepherdId, int16)

// getBlockDeviceOwnerPID returns the PID of the block device owner shepherd.
//
//go:linkname getBlockDeviceOwnerPID main.GetBlockDeviceOwnerPID
func getBlockDeviceOwnerPID() int16

// saveAndDisableIRQs disables IRQs and returns the previous DAIF state.
// Used to protect delegate queue critical sections from preemption.
//
//go:linkname saveAndDisableIRQs main.SaveAndDisableIRQs
func saveAndDisableIRQs() uint64

// restoreIRQs restores the DAIF state saved by saveAndDisableIRQs.
//
//go:linkname restoreIRQs main.RestoreIRQs
func restoreIRQs(savedDAIF uint64)

// blockForRunMaz blocks the calling thread for a RunMaz request.
//
//go:linkname blockForRunMaz main.BlockForRunMaz
func blockForRunMaz() uintptr

// blockForRunShepherd blocks the calling thread for a RunShepherd request.
//
//go:linkname blockForRunShepherd main.BlockForRunShepherd
func blockForRunShepherd() uintptr
