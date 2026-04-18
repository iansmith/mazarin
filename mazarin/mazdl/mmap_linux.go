//go:build linux

package mazdl

import (
	"syscall"
	"unsafe"
)

// reserveAndMap is the one place mazdl's MVP talks to the OS for memory.
// It reserves a contiguous anonymous VA range sized to hold every
// PT_LOAD segment (computed from the max vaddr+memsz of the ELF), then
// returns that region's base. The caller copies segment bytes into place
// and later flips permissions.
//
// When we integrate with the real shepherd, this function is replaced
// with a wrapper around SysMapELFSegment — at which point the reservation
// step goes away and each segment gets its own file-backed mapping. For
// the Linux smoke test today, anon+memcpy is the shortest path to a
// working loader.
func reserveAndMap(totalLen uintptr) (uintptr, error) {
	const prot = syscall.PROT_READ | syscall.PROT_WRITE
	const flags = syscall.MAP_PRIVATE | syscall.MAP_ANON
	p, err := syscall.Mmap(-1, 0, int(totalLen), prot, flags)
	if err != nil {
		return 0, errorf("Open", "", "", "mmap(%d): %v", totalLen, err)
	}
	return uintptr(unsafe.Pointer(&p[0])), nil
}

// mprotectSegment flips permissions on a page-aligned region after all
// writes (relocations, init-array) are complete. perms is a mask of
// segPermR/W/X.
func mprotectSegment(base, length uintptr, perms uint8) error {
	var prot int
	if perms&segPermR != 0 {
		prot |= syscall.PROT_READ
	}
	if perms&segPermW != 0 {
		prot |= syscall.PROT_WRITE
	}
	if perms&segPermX != 0 {
		prot |= syscall.PROT_EXEC
	}
	sl := unsafe.Slice((*byte)(unsafe.Pointer(base)), length)
	if err := syscall.Mprotect(sl, prot); err != nil {
		return errorf("Open", "", "", "mprotect(base=0x%x len=%d perms=0x%x): %v", base, length, perms, err)
	}
	return nil
}

const (
	segPermR uint8 = 1 << 0
	segPermW uint8 = 1 << 1
	segPermX uint8 = 1 << 2
)
