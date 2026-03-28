// Package cstr provides conversion between C-style null-terminated strings
// and Go strings. Used by shepherds that handle delegated syscalls where the
// kernel passes path arguments as null-terminated byte sequences.
package cstr

import "unsafe"

// ToGo copies a null-terminated C string starting at ptr into a Go heap
// string. It scans up to maxLen bytes for the null terminator. If no null
// is found within maxLen bytes, the entire maxLen-byte span is returned.
// Returns the empty string if ptr is 0 or maxLen is 0.
func ToGo(ptr uintptr, maxLen int) string {
	if ptr == 0 || maxLen <= 0 {
		return ""
	}
	p := unsafe.Pointer(ptr)
	n := 0
	for n < maxLen {
		if *(*byte)(unsafe.Add(p, n)) == 0 {
			break
		}
		n++
	}
	// Copy into a Go-managed heap string so the caller is not tied
	// to the lifetime of the source buffer.
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		buf[i] = *(*byte)(unsafe.Add(p, i))
	}
	return string(buf)
}

// FromGo writes a Go string into buf as a null-terminated C string.
// Returns the number of bytes written including the null terminator.
// If buf is too small the string is truncated but always null-terminated
// (minimum buf length is 1 for just the null byte).
func FromGo(s string, buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	n := len(s)
	if n >= len(buf) {
		n = len(buf) - 1 // leave room for null
	}
	copy(buf[:n], s)
	buf[n] = 0
	return n + 1
}
