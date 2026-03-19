// Package entry provides the MazarinMain entry point for .maz programs.
// This package must be imported by all .maz programs, either directly
// or via the build system.
package entry

import (
	_ "unsafe" // for go:linkname

	"mazzy/mazarin/sys"
)

// MazarinMain is the entry point called by shepherd after loading a .maz program.
// It sets up the environment and calls the program's main.main().
// This function is NOT visible to user-level programmers.
//
// The symbol name in the ELF will be "main.MazarinMain" due to go:linkname.
//
//go:linkname MazarinMain main.MazarinMain
func MazarinMain() {
	// The Go runtime is already initialized by shepherd's runtime
	// (thin client model - we share shepherd's runtime via trampolines)

	// Call the program's main.main()
	// argc=0, argv=empty - programs should use other mechanisms for args
	main_main()

	// If main.main() returns, exit with status 0
	sys.Exit(0)
}

// main_main links to the user's main.main() function
//
//go:linkname main_main main.main
func main_main()
