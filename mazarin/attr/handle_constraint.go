// handle_constraint.go — Typed constraint constructors for Handle[T].
//
// Each constructor serializes a vm.Program, creates a kernel constraint
// attribute, registers dependencies, and returns a typed Handle.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

// ConstraintI64 creates a constraint attribute that evaluates to int64.
func ConstraintI64(uri string, prog *vm.Program, deps ...HandleAny) *Handle[int64] {
	slot := createConstraintSlot(uri, flat.TypeI64, prog, deps)
	h := &Handle[int64]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindConstraint,
		typ:   flat.TypeI64,
		prog:  prog,
		toT:   func(fv flat.FlatValue) int64 { return fv.AsI64() },
		fromT: func(v int64) flat.FlatValue { return flat.NewI64(v) },
	}
	registerCascade(h)
	return h
}

// ConstraintF64 creates a constraint attribute that evaluates to float64.
func ConstraintF64(uri string, prog *vm.Program, deps ...HandleAny) *Handle[float64] {
	slot := createConstraintSlot(uri, flat.TypeF64, prog, deps)
	h := &Handle[float64]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindConstraint,
		typ:   flat.TypeF64,
		prog:  prog,
		toT:   func(fv flat.FlatValue) float64 { return fv.AsF64() },
		fromT: func(v float64) flat.FlatValue { return flat.NewF64(v) },
	}
	registerCascade(h)
	return h
}

// ConstraintBool creates a constraint attribute that evaluates to bool.
func ConstraintBool(uri string, prog *vm.Program, deps ...HandleAny) *Handle[bool] {
	slot := createConstraintSlot(uri, flat.TypeBool, prog, deps)
	h := &Handle[bool]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindConstraint,
		typ:   flat.TypeBool,
		prog:  prog,
		toT:   func(fv flat.FlatValue) bool { return fv.AsBool() },
		fromT: func(v bool) flat.FlatValue { return flat.NewBool(v) },
	}
	registerCascade(h)
	return h
}

// ConstraintStr creates a constraint attribute that evaluates to string.
func ConstraintStr(uri string, prog *vm.Program, deps ...HandleAny) *Handle[string] {
	slot := createConstraintSlot(uri, flat.TypeStr, prog, deps)
	h := &Handle[string]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindConstraint,
		typ:   flat.TypeStr,
		prog:  prog,
		isStr: true,
		toT: func(fv flat.FlatValue) string {
			ref := fv.AsStrRef()
			return sharedPR.ReadString(ref)
		},
	}
	registerCascade(h)
	return h
}

// ConstraintComposite creates a constraint attribute for a composite type.
func ConstraintComposite(uri string, flatType uint8, prog *vm.Program, deps ...HandleAny) *Handle[vm.Value] {
	slot := createConstraintSlot(uri, flatType, prog, deps)
	h := &Handle[vm.Value]{
		slot: slot,
		uri:  uri,
		kind: flat.AttrKindConstraint,
		typ:  flatType,
		prog: prog,
		toT: func(fv flat.FlatValue) vm.Value {
			v, err := flat.FlatToValue(fv, sharedPR)
			if err != nil {
				// Slot has no valid value yet (e.g., constraint returned unknown).
				// Return tribool(unknown) so callers can detect this.
				return vm.Tribool(vm.TriboolUnknown)
			}
			return v
		},
		fromT: func(v vm.Value) flat.FlatValue {
			fv, err := flat.ValueToFlat(v, sharedPR)
			if err != nil {
				panic("attr: ValueToFlat failed: " + err.Error())
			}
			return fv
		},
	}
	registerCascade(h)
	return h
}

// --- Internal helpers ---

func createConstraintSlot(uri string, valueType uint8, prog *vm.Program, deps []HandleAny) uint16 {
	// Serialize the program for storage in the bytecode region.
	bytecode := prog.Marshal()

	slot, err := sys.AttrCreate(uri, valueType, sys.AttrKindConstraint, bytecode)
	if err != nil {
		panic("attr: AttrCreate constraint failed for " + uri + ": " + err.Error())
	}

	// Register dependency edges.
	for _, dep := range deps {
		if err := sys.AttrAddDep(slot, dep.Slot()); err != nil {
			panic("attr: AttrAddDep failed: " + err.Error())
		}
	}

	return slot
}
