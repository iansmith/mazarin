#include "textflag.h"

// TestGoAsmMagic - Return a magic number (0xCAFEBABE)
// This tests that Go assembly transpilation works end-to-end
// func TestGoAsmMagic() uint64
// Return value goes in R0 (register ABI)
TEXT ·TestGoAsmMagic(SB), NOSPLIT, $0-8
    MOVD $0xCAFEBABE, R0
    RET

// TestGoAsmAdd - Add two numbers
// func TestGoAsmAdd(a, b int64) int64
// With Go 1.17+ register ABI: a in R0, b in R1, result in R0
TEXT ·TestGoAsmAdd(SB), NOSPLIT, $0-24
    ADD R0, R1, R0        // R0 = R0 + R1 (args already in registers)
    RET
