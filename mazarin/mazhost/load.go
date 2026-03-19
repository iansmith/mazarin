// Package mazhost provides host-side support for loading .maz modules
// into a shepherd's address space. It wraps the kernel's LoadMaz syscall,
// moduledata registration, MazarinShepherd interface injection, and
// returns MazarinMain as a callable func().
package mazhost

import (
	"fmt"
	"unsafe"

	merror "mazzy/mazarin/error"
	"mazzy/mazarin/sys"
)

// LoadMazBootstrap loads a .maz module using the kernel's LoadMaz syscall.
// This is used during bootstrap when the disk shepherd loads fs.maz.
// The shepherd argument is passed to the .maz's MazarinShepherd(interface{}) error
// function for interface injection (e.g., passing a BlockDevice implementation).
// On success, returns a func() that the caller should run as a goroutine.
func LoadMazBootstrap(filename string, shepherd interface{}) (func(), uintptr, *merror.Error) {
	return loadMaz(true, filename, shepherd)
}

// LoadMaz loads a .maz module via the filesystem shepherd (post-bootstrap).
// Not yet implemented — returns ErrNotImplemented.
func LoadMaz(filename string, shepherd interface{}) (func(), uintptr, *merror.Error) {
	return loadMaz(false, filename, shepherd)
}

// loadMaz is the private implementation shared by LoadMazBootstrap and LoadMaz.
func loadMaz(useKernelToLoad bool, filename string, shepherd interface{}) (func(), uintptr, *merror.Error) {
	if !useKernelToLoad {
		return nil, 0, merror.ErrNotImplemented
	}

	// Step 1: Load the .maz via kernel syscall
	result, err := sys.LoadMaz(filename)
	if err != nil {
		return nil, 0, err.Wrap(filename)
	}
	fmt.Printf("[mazhost] loaded %s: entry=0x%X base=0x%X size=0x%X\n",
		filename, result.EntryPoint, result.LoadBase, result.LoadSize)

	// Step 2: Register moduledata for stack trace support
	sys.RegisterMazModule(result)

	type funcval struct{ fn uintptr }

	// Step 3: MazarinShepherd injection deferred to caller
	if result.ShepherdInitAddr != 0 {
		fmt.Printf("[mazhost] MazarinShepherd at 0x%X\n", result.ShepherdInitAddr)
	}

	// Step 4: Build funcval for MazarinMain
	fv := &funcval{fn: uintptr(result.EntryPoint)}
	mazMain := *(*func())(unsafe.Pointer(&fv))

	return mazMain, uintptr(result.ShepherdInitAddr), nil
}
