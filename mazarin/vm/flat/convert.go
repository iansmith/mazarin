package flat

import (
	"fmt"

	"mazzy/mazarin/vm"
)

// ValueToFlat converts a heap-based vm.Value to a FlatValue.
// Strings and collections are allocated in the provided PageRegion.
func ValueToFlat(v vm.Value, pr *PageRegion) (FlatValue, error) {
	switch v.Type() {
	case vm.TypeI64:
		return NewI64(v.AsI64()), nil
	case vm.TypeF64:
		return NewF64(v.AsF64()), nil
	case vm.TypeBool:
		return NewBool(v.AsBool()), nil
	case vm.TypeTribool:
		return NewTribool(v.AsI64()), nil
	case vm.TypeStr:
		ref, err := pr.WriteString(v.AsStr())
		if err != nil {
			return FlatValue{}, err
		}
		return NewStr(ref), nil

	case vm.TypeCollI64, vm.TypeCollF64, vm.TypeCollBool, vm.TypeCollStr:
		coll := v.AsColl()
		elems := make([]FlatValue, len(coll))
		var elemType uint8
		switch v.Type() {
		case vm.TypeCollI64:
			elemType = TypeI64
		case vm.TypeCollF64:
			elemType = TypeF64
		case vm.TypeCollBool:
			elemType = TypeBool
		case vm.TypeCollStr:
			elemType = TypeStr
		}
		for i, elem := range coll {
			fe, err := ValueToFlat(elem, pr)
			if err != nil {
				return FlatValue{}, err
			}
			elems[i] = fe
		}
		ref, err := pr.WriteCollection(elemType, elems)
		if err != nil {
			return FlatValue{}, err
		}
		return NewCollection(ref), nil

	default:
		return FlatValue{}, fmt.Errorf("flat: unsupported vm type %d for conversion", v.Type())
	}
}

// FlatToValue converts a FlatValue back to a heap-based vm.Value.
// Reads string/collection data from the provided PageRegion.
func FlatToValue(fv FlatValue, pr *PageRegion) (vm.Value, error) {
	switch fv.Typ {
	case TypeI64:
		return vm.I64(fv.AsI64()), nil
	case TypeF64:
		return vm.F64(fv.AsF64()), nil
	case TypeBool:
		return vm.Bool(fv.AsBool()), nil
	case TypeTribool:
		return vm.Tribool(fv.AsTribool()), nil
	case TypeStr:
		ref := fv.AsStrRef()
		s := pr.ReadString(ref)
		return vm.Str(s), nil

	case TypeCollection:
		ref := fv.AsCollRef()
		var vmTyp uint8
		switch ref.ElemType {
		case TypeI64:
			vmTyp = vm.TypeCollI64
		case TypeF64:
			vmTyp = vm.TypeCollF64
		case TypeBool:
			vmTyp = vm.TypeCollBool
		case TypeStr:
			vmTyp = vm.TypeCollStr
		default:
			return vm.Value{}, fmt.Errorf("flat: unsupported collection element type %d for conversion", ref.ElemType)
		}
		_ = vmTyp
		elems := make([]vm.Value, ref.Count)
		for i := 0; i < int(ref.Count); i++ {
			fe := pr.ReadCollectionElement(ref, i)
			ve, err := FlatToValue(fe, pr)
			if err != nil {
				return vm.Value{}, err
			}
			elems[i] = ve
		}
		// Build the collection using the appropriate vm constructor.
		switch ref.ElemType {
		case TypeI64:
			vals := make([]int64, len(elems))
			for i, e := range elems {
				vals[i] = e.AsI64()
			}
			return vm.CollI64(vals), nil
		case TypeStr:
			vals := make([]string, len(elems))
			for i, e := range elems {
				vals[i] = e.AsStr()
			}
			return vm.CollStr(vals), nil
		default:
			// CollF64 and CollBool don't have dedicated constructors in vm,
			// so we can't convert them back losslessly. Return an error.
			return vm.Value{}, fmt.Errorf("flat: no vm constructor for collection element type %s", TypeName(ref.ElemType))
		}

	default:
		return vm.Value{}, fmt.Errorf("flat: type %s has no vm.Value equivalent", TypeName(fv.Typ))
	}
}
