package attr

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
)

// EvalCount is an atomic counter incremented each time a constraint is evaluated.
// Applications can read and reset this to measure constraint system activity.
var EvalCount atomic.Int64

// EvalNanos is an atomic accumulator of nanoseconds spent in constraint evaluation.
var EvalNanos atomic.Int64

// PanicOnCycle controls whether a dependency cycle causes a panic.
// When true (the default), Get() panics with the cycle chain.
// When false, Get() silently returns the cached value at the cycle point.
var PanicOnCycle = true

// node is the non-generic dependency graph node.
// Every Attribute[T] embeds one. The graph is walked via node pointers,
// avoiding the need for heterogeneous generic collections.
type node struct {
	dirty      bool
	eager      bool
	evaluating bool    // true while this node's compute function is running
	lastWalk   uint64  // generation of last dirty walk that visited this node
	createdAt  string  // "file.go:42" — captured at NewValue/NewConstraint
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

// evalStack tracks the chain of nodes currently being evaluated,
// used to build the cycle chain for panic messages.
var evalStack []*node

// callerLocation returns "file.go:line" for the caller's caller.
func callerLocation(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	// Trim to just filename, not full path.
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		file = file[idx+1:]
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// formatCycle builds a cycle description from evalStack starting at the
// node that was detected as a duplicate.
func formatCycle(cycleNode *node) string {
	var b strings.Builder
	b.WriteString("attr: dependency cycle detected:\n")
	// Find where the cycle starts in the stack.
	start := -1
	for i, n := range evalStack {
		if n == cycleNode {
			start = i
			break
		}
	}
	if start < 0 {
		// Shouldn't happen, but be safe.
		b.WriteString(fmt.Sprintf("  -> %s (cycle)\n", cycleNode.createdAt))
		return b.String()
	}
	for i := start; i < len(evalStack); i++ {
		b.WriteString(fmt.Sprintf("  %s\n", evalStack[i].createdAt))
	}
	b.WriteString(fmt.Sprintf("  %s  <-- cycle", cycleNode.createdAt))
	return b.String()
}

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
