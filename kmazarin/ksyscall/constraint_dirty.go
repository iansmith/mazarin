// constraint_dirty.go — Dirty propagation for the kernel attribute graph.
//
// When a value attribute is written, the kernel walks the dependency graph
// marking all transitive dependents dirty. A per-walk generation counter
// prevents exponential blowup on diamond dependencies.
//
// The walk uses iterative DFS with a fixed-size stack, making it nosplit-safe
// so it can be called from the timer IRQ top-half (exception stack).
//
// Thread waking is deferred: dirtyPropagate collects TIDs of threads blocked
// on WaitDirty into pendingDirtyWakes. Callers must flush these wakes after
// propagation — either via flushPendingDirtyWakes() (normal context) or by
// waking threads directly under the scheduler lock (top-half context).

package ksyscall

import (
	"mazzy/mazarin/vm/flat"
)

// dirtyStackSize is the maximum depth of the iterative DFS stack.
// 64 entries handles constraint graphs up to 64 levels deep, which is
// far beyond any practical UI constraint tree.
const dirtyStackSize = 64

// maxPendingDirtyWakes is the maximum number of distinct threads to wake
// per propagation pass. In practice this is bounded by the number of
// shepherds with active WaitDirty subscriptions.
const maxPendingDirtyWakes = 8

// Pending wake state — populated by dirtyPropagate, consumed by caller.
// Safe without synchronization because all callers run single-threaded
// (either under scheduler lock with IRQs disabled, or in a syscall context
// where the current thread is the only writer).
var (
	PendingDirtyWakeTIDs  [maxPendingDirtyWakes]int32
	PendingDirtyWakeCount int32
)

// dirtyPropagate marks the given attribute's dependents dirty using iterative
// DFS. The source node (whose value just changed) always walks its dependents,
// even if already dirty — because the value is new and dependents must know.
// Dependents that are already dirty terminate their branch of the walk,
// since their subtrees were already marked by a previous propagation.
//
// Thread wakes are collected in PendingDirtyWakeTIDs/PendingDirtyWakeCount.
// The caller MUST flush these after propagation — see flushPendingDirtyWakes.
//
//go:nosplit
func (mgr *KernelAttrManager) dirtyPropagate(startSlot uint16) {
	mgr.walkGen++
	gen := mgr.walkGen

	// Fixed-size DFS stack (slot IDs to visit).
	var stack [dirtyStackSize]uint16
	sp := 0

	// Mark the source node dirty and fire eager if applicable.
	src := mgr.node(startSlot)
	src.LastWalk = gen
	src.Flags |= flat.FlagDirty
	if src.Flags&flat.FlagEagerNotify != 0 {
		mgr.enqueueNotificationCollectWake(startSlot, src.Owner)
	}

	// Push source's dependents — the source value just changed.
	depCount := int(src.DependentsCount)
	depOff := src.DependentsOffset
	for i := 0; i < depCount && sp < dirtyStackSize; i++ {
		stack[sp] = mgr.readEdge(depOff, i)
		sp++
	}

	// Iterative DFS.
	for sp > 0 {
		sp--
		slot := stack[sp]

		node := mgr.node(slot)

		// Diamond dedup: skip if already visited in this walk.
		if node.LastWalk == gen {
			continue
		}
		node.LastWalk = gen

		// Already dirty — entire subtree was marked by a previous walk.
		// Eager nodes still re-enqueue so WaitDirty fires for the new value.
		if node.Flags&flat.FlagDirty != 0 {
			if node.Flags&flat.FlagEagerNotify != 0 {
				mgr.enqueueNotificationCollectWake(slot, node.Owner)
			}
			continue
		}

		// Mark dirty.
		node.Flags |= flat.FlagDirty

		// Eager notification.
		if node.Flags&flat.FlagEagerNotify != 0 {
			mgr.enqueueNotificationCollectWake(slot, node.Owner)
		}

		// Push dependents onto stack.
		depCount := int(node.DependentsCount)
		depOff := node.DependentsOffset
		for i := 0; i < depCount && sp < dirtyStackSize; i++ {
			stack[sp] = mgr.readEdge(depOff, i)
			sp++
		}
	}
}

// flushPendingDirtyWakes wakes all threads collected during dirtyPropagate.
// Must be called after each dirtyPropagate in normal (non-top-half) context.
// NOT nosplit — calls wakeDirtyNotifyThread which acquires the scheduler lock.
func flushPendingDirtyWakes() {
	n := int(PendingDirtyWakeCount)
	PendingDirtyWakeCount = 0
	for i := 0; i < n; i++ {
		wakeDirtyNotifyThread(PendingDirtyWakeTIDs[i])
	}
}

// FlushPendingDirtyWakesSchedLockHeld wakes all threads collected during
// dirtyPropagate when the caller already holds the scheduler lock with IRQs
// disabled. Used by ProcessDeadlinesTopHalf after TopHalfTickTimeUpdate.
//
//go:nosplit
func FlushPendingDirtyWakesSchedLockHeld() {
	n := int(PendingDirtyWakeCount)
	PendingDirtyWakeCount = 0
	for i := 0; i < n; i++ {
		wakeDirtyNotifyThreadSchedLockHeld(PendingDirtyWakeTIDs[i])
	}
}
