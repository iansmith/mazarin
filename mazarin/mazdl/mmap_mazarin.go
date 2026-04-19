//go:build mazhost

package mazdl

import (
	"syscall"
	"unsafe"
)

// reserveAndMap reserves a contiguous anon VA range for every PT_LOAD
// segment combined. On Mazarin this is a normal userspace mmap —
// kmazarin's SyscallMmap handles MAP_ANONYMOUS by returning a bump-
// allocated VA span backed by demand-paged frames.
func reserveAndMap(totalLen uintptr) (uintptr, error) {
	const prot = syscall.PROT_READ | syscall.PROT_WRITE
	const flags = syscall.MAP_PRIVATE | syscall.MAP_ANON
	p, err := syscall.Mmap(-1, 0, int(totalLen), prot, flags)
	if err != nil {
		return 0, errorf("Open", "", "", "mmap(%d): %v", totalLen, err)
	}
	return uintptr(unsafe.Pointer(&p[0])), nil
}

// mprotectSegment is a no-op on Mazarin. kmazarin's SyscallMprotect
// (kmazarin/ksyscall/stubs.go:SyscallMprotect) already returns 0
// without touching page tables — userspace pages are implicitly RWX.
// The W^X hardening that the Linux smoke does after segment copies is
// intentionally skipped here, matching the rest of the Mazarin memory
// model. If and when the kernel gains real mprotect support, this
// function becomes the single place to thread it in.
func mprotectSegment(base, length uintptr, perms uint8) error {
	_ = base
	_ = length
	_ = perms
	return nil
}

const (
	segPermR uint8 = 1 << 0
	segPermW uint8 = 1 << 1
	segPermX uint8 = 1 << 2
)
