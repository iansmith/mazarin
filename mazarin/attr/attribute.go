package attr

import "fmt"

// Attribute[T] is a type-safe attribute holding a value of type T.
type Attribute[T any] struct {
	n       node
	value   T
	compute func() T // nil for value attributes, non-nil for constraints
}

// nodePtr implements Noder.
func (a *Attribute[T]) nodePtr() *node { return &a.n }

// NewValue creates a value attribute with an initial value.
func NewValue[T any](initial T) *Attribute[T] {
	return &Attribute[T]{value: initial}
}

// NewConstraint creates a lazy constraint attribute.
// fn is a closure that calls Get() on its dependencies and returns the computed value.
// deps are the attributes this constraint depends on (for dirty propagation tracking).
func NewConstraint[T any](fn func() T, deps ...Noder) *Attribute[T] {
	a := &Attribute[T]{
		compute: fn,
		n: node{
			dirty: true, // start dirty so first Get() computes
		},
	}
	a.n.recompute = func() { a.Get() }
	// Register this node as a dependent of each dependency.
	for _, d := range deps {
		dn := d.nodePtr()
		dn.dependents = append(dn.dependents, &a.n)
		a.n.deps = append(a.n.deps, dn)
	}
	return a
}

// Get returns the current value. For dirty constraints, triggers recomputation.
func (a *Attribute[T]) Get() T {
	if a.compute == nil {
		return a.value
	}
	if !a.n.dirty {
		return a.value
	}
	a.value = a.compute()
	a.n.dirty = false
	return a.value
}

// Set updates the value (value attributes only), marks dependents dirty,
// and forces recomputation of eager dependents.
// Panics if called on a constraint attribute.
func (a *Attribute[T]) Set(v T) {
	if a.compute != nil {
		panic(fmt.Sprintf("attr: Set() called on constraint attribute"))
	}
	a.value = v
	// Walk all dependents marking them dirty.
	walkGen++
	for _, dep := range a.n.dependents {
		markDirty(dep, walkGen)
	}
	// Force recomputation of eager nodes after the full dirty walk.
	processEager()
}

// SetEager marks this attribute as eager. When marked dirty, its value
// will be demanded (recomputed) immediately after the dirty walk completes.
func (a *Attribute[T]) SetEager(eager bool) {
	a.n.eager = eager
}

// Eager returns whether this attribute is eager.
func (a *Attribute[T]) Eager() bool {
	return a.n.eager
}
