// attribute_value.go — Typed value constructors for Attribute[T].
//
// Each constructor creates a kernel-managed value attribute via SysAttrCreate,
// writes the initial value, and returns a typed Attribute.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"unsafe"
)

// ValueI64 creates a value attribute of type int64.
func ValueI64(uri string, initial int64) *Attribute[int64] {
	slot := createValueSlot(uri, flat.TypeI64)
	fv := flat.NewI64(initial)
	writeInitialValue(slot, &fv)
	return &Attribute[int64]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindValue,
		typ:   flat.TypeI64,
		toT:   func(fv flat.FlatValue) int64 { return fv.AsI64() },
		fromT: func(v int64) flat.FlatValue { return flat.NewI64(v) },
	}
}

// ValueF64 creates a value attribute of type float64.
func ValueF64(uri string, initial float64) *Attribute[float64] {
	slot := createValueSlot(uri, flat.TypeF64)
	fv := flat.NewF64(initial)
	writeInitialValue(slot, &fv)
	return &Attribute[float64]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindValue,
		typ:   flat.TypeF64,
		toT:   func(fv flat.FlatValue) float64 { return fv.AsF64() },
		fromT: func(v float64) flat.FlatValue { return flat.NewF64(v) },
	}
}

// ValueBool creates a value attribute of type bool.
func ValueBool(uri string, initial bool) *Attribute[bool] {
	slot := createValueSlot(uri, flat.TypeBool)
	fv := flat.NewBool(initial)
	writeInitialValue(slot, &fv)
	return &Attribute[bool]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindValue,
		typ:   flat.TypeBool,
		toT:   func(fv flat.FlatValue) bool { return fv.AsBool() },
		fromT: func(v bool) flat.FlatValue { return flat.NewBool(v) },
	}
}

// ValueStr creates a value attribute of type string.
// String writes go through SysAttrWriteString since the shepherd can't construct
// FlatStrRefs (shared pages are read-only).
func ValueStr(uri string, initial string) *Attribute[string] {
	slot := createValueSlot(uri, flat.TypeStr)
	// Write initial string via the string syscall.
	if err := sys.AttrWriteString(slot, initial, false); err != nil {
		panic("attr: ValueStr initial write failed: " + err.Error())
	}
	return &Attribute[string]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindValue,
		typ:   flat.TypeStr,
		isStr: true,
		toT: func(fv flat.FlatValue) string {
			ref := fv.AsStrRef()
			return sharedPR.ReadString(ref)
		},
	}
}

// ValueTribool creates a value attribute of type tribool (stored as int64: 0/1/2).
func ValueTribool(uri string, initial int64) *Attribute[int64] {
	slot := createValueSlot(uri, flat.TypeTribool)
	fv := flat.NewTribool(initial)
	writeInitialValue(slot, &fv)
	return &Attribute[int64]{
		slot:  slot,
		uri:   uri,
		kind:  flat.AttrKindValue,
		typ:   flat.TypeTribool,
		toT:   func(fv flat.FlatValue) int64 { return fv.AsTribool() },
		fromT: func(v int64) flat.FlatValue { return flat.NewTribool(v) },
	}
}

// ValueComposite creates a value attribute for a composite type (Rectangle,
// Point2D, Timespec, etc.) using vm.Value as the Go representation.
func ValueComposite(uri string, flatType uint8, initial vm.Value) *Attribute[vm.Value] {
	slot := createValueSlot(uri, flatType)
	fv, err := flat.ValueToFlat(initial, sharedPR)
	if err != nil {
		panic("attr: ValueComposite initial conversion failed: " + err.Error())
	}
	writeInitialValue(slot, &fv)
	return &Attribute[vm.Value]{
		slot: slot,
		uri:  uri,
		kind: flat.AttrKindValue,
		typ:  flatType,
		toT: func(fv flat.FlatValue) vm.Value {
			v, err := flat.FlatToValue(fv, sharedPR)
			if err != nil {
				panic("attr: FlatToValue failed: " + err.Error())
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
}

// ValueRectangle creates a Rectangle value attribute.
func ValueRectangle(uri string, initial vm.Value) *Attribute[vm.Value] {
	return ValueComposite(uri, flat.TypeRectangle, initial)
}

// ValuePoint2D creates a Point2D value attribute.
func ValuePoint2D(uri string, initial vm.Value) *Attribute[vm.Value] {
	return ValueComposite(uri, flat.TypePoint2D, initial)
}

// ValueTimespec creates a Timespec value attribute.
func ValueTimespec(uri string, initial vm.Value) *Attribute[vm.Value] {
	return ValueComposite(uri, flat.TypeTimespec, initial)
}

// --- Internal helpers ---

func createValueSlot(uri string, valueType uint8) uint16 {
	slot, err := sys.AttrCreate(uri, valueType, sys.AttrKindValue, nil)
	if err != nil {
		panic("attr: AttrCreate failed for " + uri + ": " + err.Error())
	}
	return slot
}

func writeInitialValue(slot uint16, fv *flat.FlatValue) {
	buf := (*[40]byte)(unsafe.Pointer(fv))
	if err := sys.AttrWrite(slot, buf); err != nil {
		panic("attr: AttrWrite initial value failed: " + err.Error())
	}
}
