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

//go:linkname setBlockDeviceOwnerPID main.SetBlockDeviceOwnerPID
func setBlockDeviceOwnerPID(pid int16)

// saveAndDisableIRQs disables IRQs and returns the previous DAIF state.
// Used to protect delegate queue critical sections from preemption.
//
//go:linkname saveAndDisableIRQs main.SaveAndDisableIRQs
func saveAndDisableIRQs() uint64

// restoreIRQs restores the DAIF state saved by saveAndDisableIRQs.
//
//go:linkname restoreIRQs main.RestoreIRQs
func restoreIRQs(savedDAIF uint64)

// submitRunShepherd submits a RunShepherd request to the kernel worker goroutine.
//
//go:linkname submitRunShepherd main.SubmitRunShepherd
func submitRunShepherd(req RunShepherdWorkRequest) uintptr

// submitEpoll submits an epoll_ctl request to the kernel worker goroutine.
//
//go:linkname submitEpoll main.SubmitEpoll
func submitEpoll(req EpollWorkRequest) uintptr

// setBlockAsyncSlot stores per-tag metadata for async block I/O completions.
// clumpAddr is the VA of *proc.DMAClump stored as uintptr (0 if no clump).
// userData is the opaque tag from io_uring SQEntry (0 for legacy BlockSubmit path).
//
//go:linkname setBlockAsyncSlot main.SetBlockAsyncSlot
func setBlockAsyncSlot(tag uint16, sidecarStatusVA uintptr, sidecarIdx uint8, dataKernelVA uintptr, dataLen uint32, clumpAddr uintptr, userData uint64)

// enableBlockAsyncMode switches the block device top-half to async completion delivery.
//
//go:linkname enableBlockAsyncMode main.EnableBlockAsyncMode
func enableBlockAsyncMode()

// Block IRQ instrumentation counters (read from SVC path, written by top-half).

//go:linkname getBlockIRQCount main.GetBlockIRQCount
func getBlockIRQCount() uint32

//go:linkname getBlockTotalDrained main.GetBlockTotalDrained
func getBlockTotalDrained() uint32

//go:linkname getBlockEmptyIRQ main.GetBlockEmptyIRQ
func getBlockEmptyIRQ() uint32

//go:linkname getBlockRingFull main.GetBlockRingFull
func getBlockRingFull() uint32

//go:linkname getBlockLastNumFree main.GetBlockLastNumFree
func getBlockLastNumFree() uint32

//go:linkname getBlockEmptyRawUsedIdx main.GetBlockEmptyRawUsedIdx
func getBlockEmptyRawUsedIdx() uint32

//go:linkname getBlockEmptyLastUsedIdx main.GetBlockEmptyLastUsedIdx
func getBlockEmptyLastUsedIdx() uint32

//go:linkname getBlockEmptyUsedPtr main.GetBlockEmptyUsedPtr
func getBlockEmptyUsedPtr() uint64

//go:linkname getBlockEmptySnapped main.GetBlockEmptySnapped
func getBlockEmptySnapped() uint32
