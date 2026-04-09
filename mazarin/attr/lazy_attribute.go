// lazy_attribute.go — LazyAttribute[T] for deferred value computation.
//
// A LazyAttribute wraps a user-provided function that produces the current
// value of T. The value is only materialized (and written to the kernel)
// when someone calls Get(). Between Get() calls, the interactor signals
// changes via MarkDirty() which is purely local — no kernel interaction.
//
// This is ideal for expensive-to-materialize values like gap buffer → string
// where edits are frequent but reads from the constraint system are infrequent.
//
// LazyAttribute is ALWAYS a value attribute. It can be the source (input) to
// constraints but never the output of one.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm/flat"
)

// LazyAttribute[T] extends Attribute[T] with lazy value computation.
type LazyAttribute[T any] struct {
	Attribute[T]
	provider   func() T // returns the current value — must not touch the constraint system
	localDirty bool     // true if provider() has a newer value than the kernel
}

// LazyStr creates a lazy string value attribute. The provider function is
// called only when Get() is invoked and the local dirty flag is set.
// The provider must not read or write any attributes.
func LazyStr(uri string, initial string, provider func() string) *LazyAttribute[string] {
	slot := createValueSlot(uri, 0x05) // flat.TypeStr
	if err := sys.AttrWriteString(slot, initial, false); err != nil {
		panic("attr: LazyStr initial write failed: " + err.Error())
	}
	la := &LazyAttribute[string]{
		Attribute: Attribute[string]{
			slot:  slot,
			uri:   uri,
			kind:  flat.AttrKindValue,
			typ:   flat.TypeStr,
			isStr: true,
			toT: func(fv flat.FlatValue) string {
				ref := fv.AsStrRef()
				return sharedPR.ReadString(ref)
			},
		},
		provider: provider,
	}
	return la
}

// MarkDirty signals that the provider has a new value. No kernel interaction
// occurs — the value is materialized lazily on the next Get().
func (la *LazyAttribute[T]) MarkDirty() {
	la.localDirty = true
}

// Get returns the current value. If locally dirty, calls the provider to get
// the current value, writes it to the kernel (which triggers dirty propagation
// to dependent constraints), and clears the local dirty flag.
func (la *LazyAttribute[T]) Get() T {
	if la.localDirty {
		la.flush()
	}
	return la.Attribute.Get()
}

// flush materializes the value and writes it to the kernel.
func (la *LazyAttribute[T]) flush() {
	la.localDirty = false
	v := la.provider()
	la.Attribute.Set(v)
}
