package vm

// AttrResolver provides attribute namespace access to the VM interpreter.
// The real implementation reads from shared pages via VDSO or syscall.
// Test implementations can use in-memory maps.
type AttrResolver interface {
	// Find matches a URI pattern against the namespace.
	// Returns matching URIs as a string slice and the query result slot index.
	Find(pattern string) (uris []string, querySlot uint16)

	// Deref resolves a URI to a typed value.
	// Returns (value, slot, true) if found with matching type,
	// or (zero, 0, false) if not found or type mismatch.
	Deref(uri string, expectedType uint8) (Value, uint16, bool)

	// Exists returns true if any attributes exist under the URI prefix,
	// along with the trie node slot for read set tracking.
	Exists(prefix string) (bool, uint16)
}

// RunWithResolver executes a program with attribute namespace access.
// Returns the results, the read set of accessed attributes, and any error.
func RunWithResolver(prog *Program, resolver AttrResolver, args ...Value) ([]Value, *ReadSet, error) {
	vm := machine{
		code:     prog.Code,
		strings:  prog.Strings,
		funcs:    prog.Funcs,
		fuel:     MaxInstructions,
		resolver: resolver,
	}
	if prog.Funcs != nil {
		vm.pc = int(prog.Funcs[prog.Entry].PC)
	}
	for _, a := range args {
		if err := vm.push(a); err != nil {
			return nil, nil, err
		}
	}
	if err := vm.exec(); err != nil {
		return nil, nil, err
	}
	return vm.stack[:vm.sp], &vm.readSet, nil
}
