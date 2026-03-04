
// helloworld is the simplest .maz userspace test program.
// It calls GetTime (Mazzy syscall) and Printf (Linux syscall via priest).
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// init forces the linker to keep MazarinMain alive. With thin stubs,
// runtime.main never reaches main.main, so MazarinMain would be DCE'd.
// Reading MazEntryPoint in init prevents the linker from treating it as
// write-only.
func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
}

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
