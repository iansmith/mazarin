// constraint.go — Client-side syscall wrappers for the constraint attribute system.

package sys

import (
	"unsafe"
)

// Constraint attribute syscall numbers (must match kernel ksyscall/mazzy.go).
const (
	sysAttrCreate        = 0x1021
	sysAttrWrite         = 0x1022
	sysAttrWriteURI      = 0x1023
	sysAttrAddDep        = 0x1024
	sysAttrUpdateDeps    = 0x1025
	sysAttrRegisterQuery = 0x1026
	sysAttrWriteResult   = 0x1027
	sysAttrWriteString   = 0x1028
	sysAttrSetEager      = 0x1029
	sysAttrWaitDirty     = 0x102A
	sysAttrIncrementI64  = 0x102B
)

// Attribute kinds (must match flat.AttrKindValue / AttrKindConstraint).
const (
	AttrKindValue      = 0
	AttrKindConstraint = 1
)

// AttrCreate creates an attribute with a URI in the kernel namespace.
// Returns the slot index on success, or an error.
func AttrCreate(uri string, valueType uint8, kind uint8, bytecode []byte) (uint16, error) {
	uriPtr := unsafe.Pointer(unsafe.StringData(uri))
	var bcPtr unsafe.Pointer
	var bcLen uintptr
	if len(bytecode) > 0 {
		bcPtr = unsafe.Pointer(&bytecode[0])
		bcLen = uintptr(len(bytecode))
	}

	r1, _, errno := RawSyscall(sysAttrCreate,
		uintptr(uriPtr), uintptr(len(uri)),
		uintptr(valueType), uintptr(kind),
		uintptr(bcPtr), bcLen)
	if errno != 0 {
		return 0, errno
	}
	return uint16(r1), nil
}

// AttrWrite writes a FlatValue (40 bytes) to an attribute by slot index.
func AttrWrite(slot uint16, value *[40]byte) error {
	_, _, errno := RawSyscall(sysAttrWrite,
		uintptr(slot),
		uintptr(unsafe.Pointer(&value[0])), 40,
		0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrWriteURI writes a FlatValue (40 bytes) to an attribute by URI string.
func AttrWriteURI(uri string, value *[40]byte) error {
	uriPtr := unsafe.Pointer(unsafe.StringData(uri))
	_, _, errno := RawSyscall(sysAttrWriteURI,
		uintptr(uriPtr), uintptr(len(uri)),
		uintptr(unsafe.Pointer(&value[0])), 40,
		0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrAddDep adds a dependency edge: fromSlot depends on toSlot.
func AttrAddDep(fromSlot, toSlot uint16) error {
	_, _, errno := RawSyscall(sysAttrAddDep,
		uintptr(fromSlot), uintptr(toSlot),
		0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrUpdateDeps replaces the full dependency set of a constraint attribute.
func AttrUpdateDeps(constraintSlot uint16, readSet []uint16) error {
	var ptr unsafe.Pointer
	if len(readSet) > 0 {
		ptr = unsafe.Pointer(&readSet[0])
	}
	_, _, errno := RawSyscall(sysAttrUpdateDeps,
		uintptr(constraintSlot),
		uintptr(ptr), uintptr(len(readSet)),
		0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrRegisterQuery registers a find pattern and returns the query result slot.
func AttrRegisterQuery(pattern string) (uint16, error) {
	patPtr := unsafe.Pointer(unsafe.StringData(pattern))
	r1, _, errno := RawSyscall(sysAttrRegisterQuery,
		uintptr(patPtr), uintptr(len(pattern)),
		0, 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return uint16(r1), nil
}

// AttrWriteResult writes a constraint evaluation result (scalar/composite) to a
// constraint slot. Does not dirty-propagate.
func AttrWriteResult(slot uint16, value *[40]byte) error {
	_, _, errno := RawSyscall(sysAttrWriteResult,
		uintptr(slot),
		uintptr(unsafe.Pointer(&value[0])), 40,
		0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrWriteString writes a string value to an attribute.
// If isConstraintResult is true, writes as a constraint result (no propagation).
// Otherwise writes as a value (with dirty propagation).
func AttrWriteString(slot uint16, s string, isConstraintResult bool) error {
	var sPtr unsafe.Pointer
	if len(s) > 0 {
		sPtr = unsafe.Pointer(unsafe.StringData(s))
	}
	var constraintFlag uintptr
	if isConstraintResult {
		constraintFlag = 1
	}
	_, _, errno := RawSyscall(sysAttrWriteString,
		uintptr(slot),
		uintptr(sPtr), uintptr(len(s)),
		constraintFlag, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrSetEager sets or clears the FlagEagerNotify flag on an attribute.
// When eager is true, dirty propagation to this attribute enqueues a
// notification that can be retrieved via AttrWaitDirty.
func AttrSetEager(slot uint16, eager bool) error {
	var eagerVal uintptr
	if eager {
		eagerVal = 1
	}
	_, _, errno := RawSyscall(sysAttrSetEager,
		uintptr(slot), eagerVal,
		0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrWaitDirty blocks until dirty notifications are available, then returns
// the dirty slot numbers. Returns the count of dirty slots, or -1 on overflow
// (caller should re-scan all eager attributes).
func AttrWaitDirty(buf []uint16) int {
	if len(buf) == 0 {
		return 0
	}
	runtime_entersyscall()
	r1, _, _ := RawSyscall(sysAttrWaitDirty,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		0, 0, 0, 0)
	runtime_exitsyscall()
	return int(int64(r1))
}

// AttrIncrementI64 atomically increments an int64 value attribute.
// Returns the new value on success, or an error.
// No dirty propagation — intended for side-effect counters.
func AttrIncrementI64(slot uint16) (int64, error) {
	r1, _, errno := RawSyscall(sysAttrIncrementI64,
		uintptr(slot), 0, 0, 0, 0, 0)
	if int64(r1) < 0 {
		return 0, errno
	}
	return int64(r1), nil
}
