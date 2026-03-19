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
// The source node (whose value just changed) always walks its dependents,
// even if already dirty — because the value is new and dependents must know.
// Dependents that are already dirty terminate their branch of the walk,
// since their subtrees were already marked by a previous propagation.
func (mgr *KernelAttrManager) dirtyPropagate(startSlot uint16) {
	mgr.walkGen++
	gen := mgr.walkGen

	// Mark the source node dirty and fire eager if applicable.
	src := mgr.node(startSlot)
	src.LastWalk = gen
	src.Flags |= flat.FlagDirty
	if src.Flags&flat.FlagEagerNotify != 0 {
		mgr.enqueueNotification(startSlot, src.Owner)
	}

	// Always walk the source's dependents — the source value just changed.
	depCount := int(src.DependentsCount)
	depOff := src.DependentsOffset
	for i := 0; i < depCount; i++ {
		depSlot := mgr.readEdge(depOff, i)
		mgr.dirtyWalk(depSlot, gen)
	}
}

// dirtyWalk recursively marks a node and its dependents dirty.
// Uses the LastWalk field on FlatAttrNode for diamond dedup.
// When a node has FlagEagerNotify, enqueues a notification for its owner.
//
// Early termination: if a node is already dirty, the entire subtree below it
// was already marked by a previous walk (dirty is only cleared by the consumer's
// Get()). Eager nodes still re-enqueue their notification so WaitDirty fires.
func (mgr *KernelAttrManager) dirtyWalk(slot uint16, gen uint64) {
	node := mgr.node(slot)

	// Diamond dedup: skip if already visited in this walk.
	if node.LastWalk == gen {
		return
	}
	node.LastWalk = gen

	// Already dirty — entire subtree was marked by a previous walk.
	// No descendant needs re-marking. Eager nodes still re-enqueue
	// so the shepherd's WaitDirty fires for the new upstream value.
	if node.Flags&flat.FlagDirty != 0 {
		if node.Flags&flat.FlagEagerNotify != 0 {
			mgr.enqueueNotification(slot, node.Owner)
		}
		return
	}

	// Mark dirty.
	node.Flags |= flat.FlagDirty

	// Eager notification: enqueue slot for the owning shepherd.
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
