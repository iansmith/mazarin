// Declarations for transpiled Go assembly functions (from Plan 9 syntax)
// These functions are defined in asm/goasm/*.s and transpiled to ELF by goasm2gnu.go

package asm

import _ "unsafe" // Required for go:linkname

// TestGoAsmMagic returns a magic number (0xCAFEBABE) to verify transpilation works
//
//go:linkname TestGoAsmMagic TestGoAsmMagic
//go:nosplit
func TestGoAsmMagic() uint64

// TestGoAsmAdd adds two numbers
//
//go:linkname TestGoAsmAdd TestGoAsmAdd
//go:nosplit
func TestGoAsmAdd(a, b int64) int64

// Runtime symbol getters (from lib_getters.s)

// GetG0Addr returns the address of runtime.g0
//
//go:linkname GetG0Addr get_g0_addr
//go:nosplit
func GetG0Addr() uintptr

// GetM0Addr returns the address of runtime.m0
//
//go:linkname GetM0Addr get_m0_addr
//go:nosplit
func GetM0Addr() uintptr

// GetGRegister returns current value of g register
//
//go:linkname GetGRegister getGRegister
//go:nosplit
func GetGRegister() uintptr

// CallMallocinit calls runtime.mallocinit from assembly
//
//go:linkname CallMallocinit call_mallocinit
//go:nosplit
func CallMallocinit()

// GetPhysPageSizeAddr returns the address of runtime.physPageSize
//
//go:linkname GetPhysPageSizeAddr get_phys_page_size_addr
//go:nosplit
func GetPhysPageSizeAddr() uintptr

// GetMcache0Addr returns the address of runtime.mcache0
//
//go:linkname GetMcache0Addr get_mcache0_addr
//go:nosplit
func GetMcache0Addr() uintptr

// GetEmptymspanAddr returns the address of runtime.emptymspan
//
//go:linkname GetEmptymspanAddr get_emptymspan_addr
//go:nosplit
func GetEmptymspanAddr() uintptr
