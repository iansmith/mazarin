package attr

// node is the non-generic dependency graph node.
// Every Attribute[T] embeds one. The graph is walked via node pointers,
// avoiding the need for heterogeneous generic collections.
type node struct {
	dirty      bool
	eager      bool
	lastWalk   uint64  // generation of last dirty walk that visited this node
	dependents []*node // attributes whose value depends on this one
	deps       []*node // attributes this one depends on (constraints only)
	recompute  func()  // calls the typed recompute on the owning Attribute[T]
}

// Noder is the interface that lets generic Attribute[T] values be passed
// as dependency references to NewConstraint. Returns the underlying node.
type Noder interface {
	nodePtr() *node
}

// eagerList collects eager nodes during a dirty walk.
// Processed and cleared after each Set() completes.
var eagerList []*node

// walkGen is incremented on each Set() to detect diamond dependencies
// within a single dirty walk without conflating with prior dirty state.
var walkGen uint64

// markDirty walks dependents depth-first, marking them dirty.
// Uses walkGen to skip already-visited nodes within a single walk,
// which handles diamond dependencies without blocking traversal
// through nodes that were dirty from a previous Set().
func markDirty(n *node, gen uint64) {
	if n.lastWalk == gen {
		return // already visited in this walk
	}
	n.lastWalk = gen
	n.dirty = true
	if n.eager {
		eagerList = append(eagerList, n)
	}
	for _, dep := range n.dependents {
		markDirty(dep, gen)
	}
}

// processEager forces recomputation of all eager nodes collected during
// the dirty walk, then clears the list.
func processEager() {
	for _, n := range eagerList {
		n.recompute()
	}
	eagerList = eagerList[:0]
}
