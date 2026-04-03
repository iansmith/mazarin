//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/ds"
	"unsafe"
)

// ============================================================================
// Channel System - Kernel-to-Shepherd Async Messaging
// ============================================================================
//
// Each shepherd has a dedicated async channel for receiving kernel messages.
// The kernel queues messages and delivers them when the shepherd calls
// WaitKernelAsync.

// ============================================================================
// Channel Constants
// ============================================================================

// Channel slot indices for each shepherd (8 slots per shepherd)
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

const MaxChannelSlotsPerShepherd = 8
const MaxChannels = MaxChannelSlotsPerShepherd * MaxShepherds // 8 * 16 = 128
const ReservedKernelChannels = MaxChannelSlotsPerShepherd   // 8 channels for kernel (shepherd 0)

// ============================================================================
// KernelAsyncBundle - Message format for kernel-to-shepherd communication
// ============================================================================

type KernelAsyncOp int32

const (
	KernelAsyncOpNone   KernelAsyncOp = 0
	KernelAsyncOpAdd    KernelAsyncOp = 1
	KernelAsyncOpRemove KernelAsyncOp = 2
)

// KernelAsyncBundle is the message format sent from kernel to shepherd.
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
	OwnerPID    ShepherdId  // Shepherd that owns this channel (-1 = kernel)
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
// Per-Shepherd Async State
// ============================================================================

// Each shepherd has one pending message slot. The kernel queues a message here,
// and it's delivered when the shepherd calls WaitKernelAsync.
var shepherdPendingMessage [MaxShepherds]KernelAsyncBundle
var shepherdHasPendingMessage [MaxShepherds]bool

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
	for i := 0; i < MaxShepherds; i++ {
		shepherdHasPendingMessage[i] = false
	}
}

// ============================================================================
// Channel Allocation
// ============================================================================

// AllocateChannel allocates a new channel for the given shepherd.
// Returns the channel ID, or -1 on failure.
//
//go:nosplit
func AllocateChannel(ownerSID ShepherdId, bundleSize int) ChannelId {
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
	ch.OwnerPID = ownerSID
	ch.Counterpart = -1

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return chanId
}

// AllocateAPIChannel allocates the API channel for a new shepherd.
// Also queues the initial ADD message for delivery on first WaitKernelAsync.
// Returns the channel ID, or -1 on failure.
//
//go:nosplit
func AllocateAPIChannel(pid ShepherdId) ChannelId {
	chanId := AllocateChannel(pid, int(unsafe.Sizeof(KernelAsyncBundle{})))
	if chanId < 0 {
		return -1
	}

	// Queue the initial ADD message for this shepherd
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

// QueueKernelAsync queues a message for delivery to the specified shepherd.
// The message will be delivered when the shepherd calls WaitKernelAsync.
// Returns true on success, false if a message is already pending.
//
//go:nosplit
func QueueKernelAsync(pid ShepherdId, bundle KernelAsyncBundle) bool {
	if pid < 0 || int(pid) >= MaxShepherds {
		return false
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if shepherdHasPendingMessage[pid] {
		// Already have a pending message - can't queue another
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return false
	}

	shepherdPendingMessage[pid] = bundle
	shepherdHasPendingMessage[pid] = true

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return true
}

// DequeueKernelAsync retrieves and clears the pending message for a shepherd.
// Returns the bundle and true if a message was pending, or empty bundle and false.
//
//go:nosplit
func DequeueKernelAsync(pid ShepherdId) (KernelAsyncBundle, bool) {
	if pid < 0 || int(pid) >= MaxShepherds {
		return KernelAsyncBundle{}, false
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if !shepherdHasPendingMessage[pid] {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return KernelAsyncBundle{}, false
	}

	bundle := shepherdPendingMessage[pid]
	shepherdHasPendingMessage[pid] = false

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
	return bundle, true
}

// HasPendingKernelAsync checks if a shepherd has a pending message.
//
//go:nosplit
func HasPendingKernelAsync(pid ShepherdId) bool {
	if pid < 0 || int(pid) >= MaxShepherds {
		return false
	}
	return shepherdHasPendingMessage[pid]
}

// ============================================================================
// Linkname Wrappers for ksyscall Package
// ============================================================================

// getCurrentThreadSIDWrapper returns the PID of the currently running thread.
// Called via linkname from ksyscall package.
//
//go:nosplit
//go:noinline
func getCurrentThreadSIDWrapper() int16 {
	t := GetCurrentThread()
	if t == nil {
		return 0 // kernel context (no thread) uses PID 0
	}
	return int16(t.PID)
}

// getCurrentThreadSlotWrapper returns the shepherd list slot (ShepherdIdx) of the
// currently running thread. Called via linkname from ksyscall package.
// Returns -1 for kernel threads (no shepherd).
//
//go:nosplit
//go:noinline
func getCurrentThreadSlotWrapper() int16 {
	t := GetCurrentThread()
	if t == nil {
		return -1
	}
	return t.ShepherdIdx
}

// dequeueKernelAsyncWrapper wraps DequeueKernelAsync for ksyscall linkname access.
// The bundle type is duplicated in ksyscall, but the memory layout is identical.
//
//go:nosplit
//go:noinline
func dequeueKernelAsyncWrapper(pid int16) (KernelAsyncBundle, bool) {
	return DequeueKernelAsync(ShepherdId(pid))
}
