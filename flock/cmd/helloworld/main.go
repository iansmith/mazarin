
// helloworld is the simplest .maz userspace test program.
// It calls GetTime (Mazzy syscall) and Printf (Linux syscall via priest).
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
)

// MazEntryPoint is a global function pointer that prevents the Go linker
// from dead-code-eliminating MazarinMain in .maz builds. The thin overlay
// stubs cause the compiler to optimize away the runtime.main → main_main
// code path, so without this reference MazarinMain would be unreachable.
var MazEntryPoint func() = MazarinMain

// MazarinMain is the entry point called by the priest when this .maz is loaded.
// The priest finds this symbol in the ELF symbol table and launches it as a goroutine.
//
//go:noinline
func MazarinMain() {
	t, err := sys.GetTime()
	if err != nil {
		fmt.Printf("GetTime error: %v\n", err)
		return
	}
	fmt.Printf("hello world %d.%09d\n", t.Seconds, t.Nanoseconds)
}

func main() {
	MazarinMain()
}
