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
