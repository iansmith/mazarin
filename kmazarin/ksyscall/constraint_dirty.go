// constraint_dirty.go — Dirty propagation for the kernel attribute graph.
//
// When a value attribute is written, the kernel walks the dependency graph
// marking all transitive dependents dirty. A per-walk generation counter
// prevents exponential blowup on diamond dependencies.

package ksyscall

import (
	"mazzy/mazarin/vm/flat"
)

// dirtyPropagate marks the given attribute's dependents dirty using DFS.
func (mgr *KernelAttrManager) dirtyPropagate(startSlot uint16) {
	mgr.walkGen++
	mgr.dirtyWalk(startSlot, mgr.walkGen)
}

// dirtyWalk recursively marks a node and its dependents dirty.
// Uses the LastWalk field on FlatAttrNode for diamond dedup.
// When a node has FlagEagerNotify, enqueues a notification for its owner.
func (mgr *KernelAttrManager) dirtyWalk(slot uint16, gen uint64) {
	node := mgr.node(slot)

	// Diamond dedup: skip if already visited in this walk.
	if node.LastWalk == gen {
		return
	}
	node.LastWalk = gen

	// Mark dirty.
	node.Flags |= flat.FlagDirty

	// Eager notification: enqueue slot for the owning priest.
	if node.Flags&flat.FlagEagerNotify != 0 {
		mgr.enqueueNotification(slot, node.Owner)
	}

	// Walk dependents.
	depCount := int(node.DependentsCount)
	depOff := node.DependentsOffset
	for i := 0; i < depCount; i++ {
		depSlot := mgr.readEdge(depOff, i)
		mgr.dirtyWalk(depSlot, gen)
	}
}
