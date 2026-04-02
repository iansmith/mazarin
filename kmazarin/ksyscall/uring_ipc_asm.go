package ksyscall

import (
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
	_ "unsafe" // for go:linkname
)

// Forward declarations for uring IPC functions in the main package.

//go:linkname blockForUringRecv main.BlockForUringRecv
func blockForUringRecv(shepherdIdx int, bufPtr uint64) uintptr

//go:linkname uringSendKernel main.UringSendKernel
func uringSendKernel(senderSID, targetSID int16, msgKVA uintptr) (int64, uintptr)

//go:linkname drainUringIPCRing main.drainUringIPCRing
func drainUringIPCRing(sid int16) (uintptr, bool)

//go:linkname advanceUringHead main.advanceUringHead
func advanceUringHead(sid int16)

//go:linkname releaseUringConnection main.ReleaseUringConnection
func releaseUringConnection(handle int, callerSID int16) int64

// uringConnectWorkRequest mirrors main.UringConnectWorkRequest for the linkname bridge.
type uringConnectWorkRequest struct {
	TargetUringID uint64
	CallerSID     int16
}

//go:linkname submitUringConnect main.SubmitUringConnect
func submitUringConnect(req uringConnectWorkRequest) uintptr

//go:linkname allocateUringIPCRing main.AllocUringIPCRing
func allocateUringIPCRing(shepherd *proc.Shepherd) bool

//go:linkname registerUringID main.RegisterUringID
func registerUringID(id uint64, sid int16)

//go:linkname allocateUringID main.AllocateUringID
func allocateUringID() uint64

// Ensure ipc package is imported (compile-time dependency for UringIPCSlotSize, etc.)
var _ = ipc.UringIPCSlotSize
