// attribute.go — Attribute[T] type for kernel-managed constraint attributes.
//
// Attribute[T] wraps a kernel attribute slot with type-safe Get/Set operations.
// Reads use the seqlock protocol on shared pages (VDSO, no syscall).
// Writes go through kernel syscalls for dirty propagation.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"unsafe"
)

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// AttributeAny is a type-erased interface for Attribute[T] that exposes the slot index.
// Used for dependency registration where the T type parameter doesn't matter.
type AttributeAny interface {
	Slot() uint16
	URI() string
}

// Attribute[T] wraps a kernel-managed attribute slot with type-safe access.
type Attribute[T any] struct {
	slot    uint16      // shared page node slot
	uri     string      // attribute URI
	kind    uint8       // flat.AttrKindValue or flat.AttrKindConstraint
	typ     uint8       // flat type tag (flat.TypeI64, etc.)
	prog    *vm.Program // cached deserialized bytecode (constraints only)
	lastRS  *vm.ReadSet // last read set from evaluation (constraints only)
	eager   bool        // local eager flag (no kernel effect until Phase 6)
	toT     func(flat.FlatValue) T // convert flat value to typed T
	fromT   func(T) flat.FlatValue // convert typed T to flat value (nil for strings)
	isStr   bool                   // true if this attribute manages string type
}

// registerCascade registers a constraint attribute for cascading evaluation.
// When another constraint derefs this attribute's slot, the resolver will
// evaluate it first if dirty.
func registerCascade[T any](h *Attribute[T]) {
	registerConstraintEvaluator(h.slot, func() {
		if h.IsDirty() {
			h.evaluate()
		}
	})
}

// Slot returns the shared page slot index.
func (h *Attribute[T]) Slot() uint16 { return h.slot }

// URI returns the attribute's namespace URI.
func (h *Attribute[T]) URI() string { return h.uri }

// IsDirty reads the dirty flag from the shared page via seqlock.
func (h *Attribute[T]) IsDirty() bool {
	node := sharedPR.Node(int16(h.slot))
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue // writer in progress, spin
		}
		dirty := node.IsDirty()
		if node.SeqCounter == seq {
			return dirty
		}
	}
}

// SetEager marks this attribute for eager dirty notification. When enabled, dirty
// propagation to this attribute enqueues a notification that can be retrieved
// via WaitDirty(). Sets both the local flag and the shared-page FlagEagerNotify.
func (h *Attribute[T]) SetEager(eager bool) {
	h.eager = eager
	sys.AttrSetEager(h.slot, eager)
}

// Get returns the current value. For constraint attributes, evaluates the
// bytecode if dirty. For value attributes, reads directly from shared page.
func (h *Attribute[T]) Get() T {
	if h.kind == flat.AttrKindConstraint && h.IsDirty() {
		h.evaluate()
	}
	fv := h.seqlockRead()
	return h.toT(fv)
}

// IsConstraint returns true if this attribute is driven by a constraint program.
func (h *Attribute[T]) IsConstraint() bool {
	return h.kind == flat.AttrKindConstraint
}

// Set writes a new value to a value attribute. Panics on constraint attributes.
func (h *Attribute[T]) Set(v T) {
	if h.kind == flat.AttrKindConstraint {
		panic("attr: cannot Set() on a constraint attribute")
	}
	if h.isStr {
		// String values go through the special string syscall.
		var s string
		// Use unsafe to extract string from T (we know T is string for isStr attributes).
		s = *(*string)(unsafe.Pointer(&v))
		if err := sys.AttrWriteString(h.slot, s, false); err != nil {
			panic("attr: AttrWriteString failed: " + err.Error())
		}
		return
	}
	fv := h.fromT(v)
	buf := (*[40]byte)(unsafe.Pointer(&fv))
	if err := sys.AttrWrite(h.slot, buf); err != nil {
		panic("attr: AttrWrite failed: " + err.Error())
	}
}

// ReadI64 reads an int64 attribute directly from the shared constraint page
// by URI, using trie lookup + seqlock. This is a pure memory read — no
// syscall, no constraint evaluation, no writeback. Returns (value, true)
// on success or (0, false) if the URI is not found.
func ReadI64(uri string) (int64, bool) {
	slot, found := trieLookupShared(uri)
	if !found {
		return 0, false
	}
	node := sharedPR.Node(int16(slot))
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue
		}
		val := node.CachedValue
		if node.SeqCounter == seq {
			return val.AsI64(), true
		}
	}
}

// seqlockRead performs a seqlock-protected read of the CachedValue from the
// shared page. Spins until a consistent read is obtained.
func (h *Attribute[T]) seqlockRead() flat.FlatValue {
	node := sharedPR.Node(int16(h.slot))
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue // writer in progress, spin
		}
		val := node.CachedValue
		if node.SeqCounter == seq {
			return val
		}
	}
}

// evaluate runs the constraint's bytecode program, writes the result back to
// the kernel, and updates dependencies if the read set changed.
func (h *Attribute[T]) evaluate() {
	t0 := nanotime()
	defer func() {
		dt := nanotime() - t0
		EvalCount.Add(1)
		EvalNanos.Add(dt)
	}()
	if h.prog == nil {
		// Deserialize bytecode on first evaluation.
		node := sharedPR.Node(int16(h.slot))
		bcOff := node.ProgramOffset
		bcLen := node.ProgramLen
		if bcLen == 0 {
			return // no program
		}
		// The bytecode region contains the full serialized Program (MZBC blob).
		// ProgramOffset is byte offset, ProgramLen is total byte length.
		totalBytes := int(bcLen)
		data := sharedPR.Bytecode[bcOff : uint32(bcOff)+uint32(totalBytes)]
		prog, err := vm.UnmarshalProgram(data)
		if err != nil {
			// Fallback: raw instruction bytes (legacy format without header).
			// Build a minimal Program from raw instruction bytes.
			prog = unmarshalLegacyInstructions(data, int(bcLen))
		}
		h.prog = prog
	}

	// Build resolver backed by shared pages.
	resolver := &sharedResolver{
		pr: sharedPR,
	}

	// Run the VM with the resolver.
	results, readSet, err := vm.RunWithResolver(h.prog, resolver)
	if err != nil {
		panic("attr: constraint VM execution failed: " + err.Error())
	}
	if len(results) == 0 {
		return // no result
	}

	// Write result back to kernel.
	// If the result is Tribool(unknown), the constraint could not resolve
	// its dependencies (e.g., remote attribute not yet published). Skip
	// the write-back and leave the cached value unchanged. Dependencies
	// are still updated so re-evaluation triggers when the remote appears.
	result := results[0]
	if result.Type() == vm.TypeTribool && result.AsI64() == vm.TriboolUnknown {
		goto updateDeps
	}
	if result.Type() == vm.TypeStr {
		// String results go through the special string syscall.
		if err := sys.AttrWriteString(h.slot, result.AsStr(), true); err != nil {
			panic("attr: AttrWriteString (constraint result) failed: " + err.Error())
		}
	} else {
		// Convert vm.Value to FlatValue and write via syscall.
		fv, err := flat.ValueToFlat(result, sharedPR)
		if err != nil {
			panic("attr: ValueToFlat failed: " + err.Error())
		}
		buf := (*[40]byte)(unsafe.Pointer(&fv))
		if err := sys.AttrWriteResult(h.slot, buf); err != nil {
			panic("attr: AttrWriteResult failed: " + err.Error())
		}
	}

updateDeps:
	// Update dependencies if the read set changed.
	if readSet != nil {
		if h.lastRS == nil || !h.lastRS.Equal(readSet) {
			slots := make([]uint16, readSet.Count)
			copy(slots, readSet.Slots[:readSet.Count])
			if err := sys.AttrUpdateDeps(h.slot, slots); err != nil {
				panic("attr: AttrUpdateDeps failed: " + err.Error())
			}
			h.lastRS = readSet
		}
	}
}

// unmarshalLegacyInstructions builds a minimal Program from raw instruction bytes
// (the pre-Phase 5 format where ProgramLen is instruction count).
func unmarshalLegacyInstructions(data []byte, instCount int) *vm.Program {
	code := make([]vm.Inst, instCount)
	for i := 0; i < instCount; i++ {
		off := i * 16
		if off+16 > len(data) {
			break
		}
		code[i] = vm.Inst{
			Opcode: data[off],
			Typ:    data[off+1],
			Op1:    uint16(data[off+2]) | uint16(data[off+3])<<8,
			Op2:    uint16(data[off+4]) | uint16(data[off+5])<<8,
			Flags:  uint16(data[off+6]) | uint16(data[off+7])<<8,
			Imm:    uint64(data[off+8]) | uint64(data[off+9])<<8 | uint64(data[off+10])<<16 | uint64(data[off+11])<<24 |
				uint64(data[off+12])<<32 | uint64(data[off+13])<<40 | uint64(data[off+14])<<48 | uint64(data[off+15])<<56,
		}
	}
	return &vm.Program{Code: code}
}
