// death_subscribe.go - SysSubscribeDeaths: global shepherd death notification.
//
// Shepherds call SysSubscribeDeaths to register for ProtoDeath messages
// whenever ANY shepherd dies (not just uring peers). The kernel maintains
// a small subscriber list and delivers notifications via the subscriber's
// existing uring ring.
package ksyscall

import (
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
)

// deathSubscriber is an entry in the global death subscriber list.
type deathSubscriber struct {
	InUse bool
	SID   int16
}

// deathSubscribers holds the global subscriber list. Max one entry per shepherd.
var deathSubscribers [proc.MaxLiveShepherds]deathSubscriber

// SyscallSubscribeDeaths registers the calling shepherd for global death
// notifications. ProtoDeath messages are delivered to the caller's uring ring
// whenever any other shepherd dies.
//
// No arguments. Returns 0 on success, -ENOSPC if subscriber list is full.
//
//go:noinline
func SyscallSubscribeDeaths(_, _, _, _, _, _ uint64) int64 {
	callerSID := int16(proc.CurrentShepherd().PID)

	// Check for duplicate registration.
	for i := range deathSubscribers {
		if deathSubscribers[i].InUse && deathSubscribers[i].SID == callerSID {
			return 0 // already subscribed, idempotent
		}
	}

	// Find a free slot.
	for i := range deathSubscribers {
		if !deathSubscribers[i].InUse {
			deathSubscribers[i] = deathSubscriber{InUse: true, SID: callerSID}
			return 0
		}
	}

	return -28 // ENOSPC
}

// CleanupDeathSubscriptionsForShepherd removes all subscription entries for
// the given shepherd. Called from TerminateShepherd during cleanup.
func CleanupDeathSubscriptionsForShepherd(sid int16) {
	for i := range deathSubscribers {
		if deathSubscribers[i].InUse && deathSubscribers[i].SID == sid {
			deathSubscribers[i] = deathSubscriber{}
		}
	}
}

// NotifyDeathSubscribers sends a ProtoDeath message to all subscribers
// except the dead shepherd itself. Called from TerminateShepherd AFTER
// CleanupUringIPCForShepherd (peers get notified first, then subscribers).
//
// writeToRing is the kernel ring writer function, injected to avoid an
// import cycle (kmazarin/kmazarin → kmazarin/ksyscall).
func NotifyDeathSubscribers(deadSID int16, writeToRing func(int16, *ipc.UringIPCMsg)) {
	deathMsg := ipc.EncodeDeathNotification(deadSID)
	for i := range deathSubscribers {
		sub := &deathSubscribers[i]
		if sub.InUse && sub.SID != deadSID {
			writeToRing(sub.SID, &deathMsg)
		}
	}
}
