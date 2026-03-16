package inmem

import (
	"mazzy/mazarin/vm"
)

// VMDep pairs an attribute dependency with a function that extracts its
// current value as a vm.Value. The dependency order must match the
// compiled program's parameter order.
type VMDep struct {
	dep     Noder
	extract func() vm.Value
}

// DepI64 creates a VMDep for an int64 attribute.
func DepI64(a *Attribute[int64]) VMDep {
	return VMDep{dep: a, extract: func() vm.Value { return vm.I64(a.Get()) }}
}

// DepF64 creates a VMDep for a float64 attribute.
func DepF64(a *Attribute[float64]) VMDep {
	return VMDep{dep: a, extract: func() vm.Value { return vm.F64(a.Get()) }}
}

// DepBool creates a VMDep for a bool attribute.
func DepBool(a *Attribute[bool]) VMDep {
	return VMDep{dep: a, extract: func() vm.Value { return vm.Bool(a.Get()) }}
}

// DepStr creates a VMDep for a string attribute.
func DepStr(a *Attribute[string]) VMDep {
	return VMDep{dep: a, extract: func() vm.Value { return vm.Str(a.Get()) }}
}

// newVMCompute builds a closure that collects dependency values and runs
// the VM program, returning the first result.
func newVMCompute(prog *vm.Program, deps []VMDep) func() vm.Value {
	return func() vm.Value {
		args := make([]vm.Value, len(deps))
		for i, d := range deps {
			args[i] = d.extract()
		}
		results, err := vm.Run(prog, args...)
		if err != nil {
			panic("attr: VM constraint execution failed: " + err.Error())
		}
		if len(results) == 0 {
			panic("attr: VM constraint returned no values")
		}
		return results[0]
	}
}

// noders extracts the Noder slice from VMDeps for dependency registration.
func noders(deps []VMDep) []Noder {
	ns := make([]Noder, len(deps))
	for i, d := range deps {
		ns[i] = d.dep
	}
	return ns
}

// NewVMConstraintI64 creates a constraint backed by a compiled VM program
// that returns an int64.
func NewVMConstraintI64(prog *vm.Program, deps ...VMDep) *Attribute[int64] {
	run := newVMCompute(prog, deps)
	return NewConstraint(func() int64 {
		return run().AsI64()
	}, noders(deps)...)
}

// NewVMConstraintF64 creates a constraint backed by a compiled VM program
// that returns a float64.
func NewVMConstraintF64(prog *vm.Program, deps ...VMDep) *Attribute[float64] {
	run := newVMCompute(prog, deps)
	return NewConstraint(func() float64 {
		return run().AsF64()
	}, noders(deps)...)
}

// NewVMConstraintBool creates a constraint backed by a compiled VM program
// that returns a bool.
func NewVMConstraintBool(prog *vm.Program, deps ...VMDep) *Attribute[bool] {
	run := newVMCompute(prog, deps)
	return NewConstraint(func() bool {
		return run().AsBool()
	}, noders(deps)...)
}

// NewVMConstraintStr creates a constraint backed by a compiled VM program
// that returns a string.
func NewVMConstraintStr(prog *vm.Program, deps ...VMDep) *Attribute[string] {
	run := newVMCompute(prog, deps)
	return NewConstraint(func() string {
		return run().AsStr()
	}, noders(deps)...)
}
