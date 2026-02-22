//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/ds"
	"unsafe"
)

// ============================================================================
// Channel System - Kernel-to-Priest Async Messaging
// ============================================================================
//
// Each priest has a dedicated async channel for receiving kernel messages.
// The kernel queues messages and delivers them when the priest calls
// WaitKernelAsync.

// ============================================================================
// Channel Constants
// ============================================================================

// Channel slot indices for each priest (8 slots per priest)
const (
	ChannelSlotAPI      = 0 // API channel for kernel communication
	ChannelSlotReserved1 = 1
	ChannelSlotReserved2 = 2
	ChannelSlotReserved3 = 3
	ChannelSlotReserved4 = 4
	ChannelSlotReserved5 = 5
	ChannelSlotReserved6 = 6
	ChannelSlotReserved7 = 7
)

const MaxChannelSlotsPerPriest = 8
const MaxChannels = MaxChannelSlotsPerPriest * MaxPriests // 8 * 16 = 128
const ReservedKernelChannels = MaxChannelSlotsPerPriest   // 8 channels for kernel (priest 0)

// ============================================================================
// KernelAsyncBundle - Message format for kernel-to-priest communication
// ============================================================================

type KernelAsyncOp int32

const (
	KernelAsyncOpNone   KernelAsyncOp = 0
	KernelAsyncOpAdd    KernelAsyncOp = 1
	KernelAsyncOpRemove KernelAsyncOp = 2
)

// KernelAsyncBundle is the message format sent from kernel to priest.
// This struct must match the userspace definition in mazarin/sys/async.go.
type KernelAsyncBundle struct {
	Op        KernelAsyncOp // Operation type (Add, Remove)
	ChannelId int32         // Target channel ID
	Message   [4]int32      // 4-int message payload
}

// ============================================================================
// Channel Structure
// ============================================================================

type ChannelId int16

// Channel represents a communication endpoint.
type Channel struct {
	ChanId      ChannelId // Unique channel ID
	BundleSize  int       // Size of bundles for this channel
	OwnerPID    PriestId  // Priest that owns this channel (-1 = kernel)
	Counterpart ChannelId // Connected channel (-1 = none)
}

// Id implements ds.Ider interface
func (c *Channel) Id() int32 {
	return int32(c.ChanId)
}

// ============================================================================
// Static Allocation for Channels
// ============================================================================

var channelListData [MaxChannels]Channel
var channelListInUse [MaxChannels]bool
var channelList ds.StaticList[*Channel, Channel]

var channelIdStackData [MaxChannels]ChannelId
var channelIdAllocator ds.StaticAllocator[ChannelId]

// ============================================================================
// Per-Priest Async State
// ============================================================================

// Each priest has one pending message slot. The kernel queues a message here,
// and it's delivered when the priest calls WaitKernelAsync.
var priestPendingMessage [MaxPriests]KernelAsyncBundle
var priestHasPendingMessage [MaxPriests]bool

// ============================================================================
// Initialization
// ============================================================================

// InitChannels initializes the channel subsystem.
// Called during kernel startup.
func InitChannels() {
	// Initialize channel list
	channelList = ds.StaticList[*Channel, Channel]{
		Data:  channelListData[:],
		InUse: channelListInUse[:],
	}
	channelList.InitReserved(ReservedKernelChannels)

	// Initialize channel ID allocator
	// Reserve first 8 IDs for kernel channels
	channelIdAllocator.InitWithReserved(channelIdStackData[:], ReservedKernelChannels)

	// Clear pending message state
	for i := 0; i < MaxPriests; i++ {
		priestHasPendingMessage[i] = false
	}
}

// ============================================================================
// Channel Allocation
// ============================================================================

// AllocateChannel allocates a new channel for the given priest.
// Returns the channel ID, or -1 on failure.
//
//go:nosplit
func AllocateChannel(ownerPID PriestId, bundleSize int) ChannelId {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Allocate from static list
	_, ch := channelList.Allocate()
	if ch == nil {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return -1
	}

	// Acquire unique ID
	chanId := channelIdAllocator.Acquire()

	// Initialize channel
	ch.ChanId = chanId
	ch.BundleSize = bundleSize
	ch.OwnerPID = ownerPID
	ch.Counterpart = -1

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return chanId
}

// AllocateAPIChannel allocates the API channel for a new priest.
// Also queues the initial ADD message for delivery on first WaitKernelAsync.
// Returns the channel ID, or -1 on failure.
//
//go:nosplit
func AllocateAPIChannel(pid PriestId) ChannelId {
	chanId := AllocateChannel(pid, int(unsafe.Sizeof(KernelAsyncBundle{})))
	if chanId < 0 {
		return -1
	}

	// Queue the initial ADD message for this priest
	QueueKernelAsync(pid, KernelAsyncBundle{
		Op:        KernelAsyncOpAdd,
		ChannelId: int32(chanId),
		Message:   [4]int32{0, 0, 0, 0},
	})

	return chanId
}

// ============================================================================
// Message Queuing
// ============================================================================

// QueueKernelAsync queues a message for delivery to the specified priest.
// The message will be delivered when the priest calls WaitKernelAsync.
// Returns true on success, false if a message is already pending.
//
//go:nosplit
func QueueKernelAsync(pid PriestId, bundle KernelAsyncBundle) bool {
	if pid < 0 || int(pid) >= MaxPriests {
		return false
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if priestHasPendingMessage[pid] {
		// Already have a pending message - can't queue another
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return false
	}

	priestPendingMessage[pid] = bundle
	priestHasPendingMessage[pid] = true

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return true
}

// DequeueKernelAsync retrieves and clears the pending message for a priest.
// Returns the bundle and true if a message was pending, or empty bundle and false.
//
//go:nosplit
func DequeueKernelAsync(pid PriestId) (KernelAsyncBundle, bool) {
	if pid < 0 || int(pid) >= MaxPriests {
		return KernelAsyncBundle{}, false
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if !priestHasPendingMessage[pid] {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return KernelAsyncBundle{}, false
	}

	bundle := priestPendingMessage[pid]
	priestHasPendingMessage[pid] = false

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return bundle, true
}

// HasPendingKernelAsync checks if a priest has a pending message.
//
//go:nosplit
func HasPendingKernelAsync(pid PriestId) bool {
	if pid < 0 || int(pid) >= MaxPriests {
		return false
	}
	return priestHasPendingMessage[pid]
}

// ============================================================================
// Linkname Wrappers for ksyscall Package
// ============================================================================

// getCurrentThreadPIDWrapper returns the PID of the currently running thread.
// Called via linkname from ksyscall package.
//
//go:nosplit
//go:noinline
func getCurrentThreadPIDWrapper() int16 {
	t := GetCurrentThread()
	if t == nil {
		return 0 // kernel context (no thread) uses PID 0
	}
	return int16(t.PID)
}

// dequeueKernelAsyncWrapper wraps DequeueKernelAsync for ksyscall linkname access.
// The bundle type is duplicated in ksyscall, but the memory layout is identical.
//
//go:nosplit
//go:noinline
func dequeueKernelAsyncWrapper(pid int16) (KernelAsyncBundle, bool) {
	return DequeueKernelAsync(PriestId(pid))
}
