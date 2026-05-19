
// helloworld is the simplest .maz userspace test program.
// It calls GetTime (Mazzy syscall) and Printf (Linux syscall via shepherd).
package main

import (
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
)

func init() { mazhost.PinEntry(MazarinMain, nil) }

// MazarinMain is the entry point called by the shepherd when this .maz is loaded.
// The shepherd finds this symbol in the ELF symbol table and launches it as a goroutine.
//
//go:noinline
func MazarinMain() {
	// Use raw syscall writes to avoid fmt/os.Stdout (nil in .maz — init doesn't run)
	msg := []byte("hello world from .maz!\n")
	for _, ch := range msg {
		sys.RawWrite(1, ch)
	}
}

func main() {
	MazarinMain()
}
