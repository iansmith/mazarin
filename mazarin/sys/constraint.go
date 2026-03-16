// constraint.go — Client-side syscall wrappers for the constraint attribute system.

package sys

import (
	"syscall"
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
	result := int64(r1)
	if result < 0 {
		return 0, syscall.Errno(-result)
	}
	if errno != 0 {
		return 0, errno
	}
	return uint16(result), nil
}

// AttrWrite writes a FlatValue (32 bytes) to an attribute by slot index.
func AttrWrite(slot uint16, value *[32]byte) error {
	r1, _, errno := RawSyscall(sysAttrWrite,
		uintptr(slot),
		uintptr(unsafe.Pointer(&value[0])), 32,
		0, 0, 0)
	result := int64(r1)
	if result < 0 {
		return syscall.Errno(-result)
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrWriteURI writes a FlatValue (32 bytes) to an attribute by URI string.
func AttrWriteURI(uri string, value *[32]byte) error {
	uriPtr := unsafe.Pointer(unsafe.StringData(uri))
	r1, _, errno := RawSyscall(sysAttrWriteURI,
		uintptr(uriPtr), uintptr(len(uri)),
		uintptr(unsafe.Pointer(&value[0])), 32,
		0, 0)
	result := int64(r1)
	if result < 0 {
		return syscall.Errno(-result)
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// AttrAddDep adds a dependency edge: fromSlot depends on toSlot.
func AttrAddDep(fromSlot, toSlot uint16) error {
	r1, _, errno := RawSyscall(sysAttrAddDep,
		uintptr(fromSlot), uintptr(toSlot),
		0, 0, 0, 0)
	result := int64(r1)
	if result < 0 {
		return syscall.Errno(-result)
	}
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
	r1, _, errno := RawSyscall(sysAttrUpdateDeps,
		uintptr(constraintSlot),
		uintptr(ptr), uintptr(len(readSet)),
		0, 0, 0)
	result := int64(r1)
	if result < 0 {
		return syscall.Errno(-result)
	}
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
	result := int64(r1)
	if result < 0 {
		return 0, syscall.Errno(-result)
	}
	if errno != 0 {
		return 0, errno
	}
	return uint16(result), nil
}
