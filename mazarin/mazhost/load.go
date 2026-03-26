// Package mazhost provides host-side support for loading .maz modules
// into a shepherd's address space. It wraps the kernel's LoadMaz syscall,
// moduledata registration, MazarinShepherd interface injection, and
// returns MazarinMain as a callable func().
package mazhost

import (
	"fmt"
	"runtime"
	"syscall"
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
	return loadMazInternal(true, filename, shepherd)
}

// LaunchMaz loads a .maz module by name and runs its MazarinMain in a new
// goroutine with a pre-grown stack. This is the standard way to load .maz
// modules — it handles LoadMazByName, LoadMaz, RegisterMazModule, funcval
// construction, and runWithLargeStack in one call.
func LaunchMaz(name string) {
	path := sys.LoadMazByName("/" + name)
	fmt.Printf("[mazhost] LaunchMaz(%q): loading %s...\n", name, path)
	mazMain, _, err := loadMazInternal(true, path, nil)
	if err != nil {
		panic(fmt.Sprintf("[mazhost] LaunchMaz(%q) failed: %v", name, err))
	}
	go runWithLargeStack(mazMain)
	fmt.Printf("[mazhost] LaunchMaz(%q): goroutine launched\n", name)
}

// RunMaz runs an already-loaded .maz func on a pre-grown stack.
// Used by disk shepherd after LoadMazBootstrap + MazarinShepherd injection.
func RunMaz(fn func()) {
	runWithLargeStack(fn)
}

// runWithLargeStack allocates a 256KB stack frame before calling fn,
// preventing .maz code from hitting its broken morestack (which hangs
// forever due to uninitialized runtime globals in the PIE binary).
// The buffer is kept alive across fn() so GC's shrinkstack doesn't
// shrink the goroutine stack while .maz code is running.
//
//go:noinline
func runWithLargeStack(fn func()) {
	var buf [262144]byte
	buf[0] = 1
	buf[len(buf)-1] = 1
	if buf[131072] != 0 {
		panic("unreachable")
	}
	fn()
	runtime.KeepAlive(&buf)
}

// loadMazInternal is the private implementation shared by LoadMazBootstrap and LaunchMaz.
// Tries the LoadFile delegate first (fs.maz → async DMA block device → zero-copy
// page transfer → LoadMazFromPages). Falls back to LoadMaz (kernel direct FAT32 mount)
// when the delegate is not yet registered (e.g., loading fs.maz itself during bootstrap).
func loadMazInternal(useKernelToLoad bool, filename string, shepherd interface{}) (func(), uintptr, *merror.Error) {
	if !useKernelToLoad {
		return nil, 0, merror.ErrNotImplemented
	}

	// Step 1: Try delegate path first (fs.maz serves file via async DMA)
	var result *sys.MazLoadResult
	var err *merror.Error

	lf, lfErr := sys.LoadFile(filename)
	if lfErr == nil && lf.StartVA != 0 {
		fmt.Printf("[mazhost] LoadFile(%s): %d pages (%d bytes) — using async delegate path\n",
			filename, lf.NumPages, lf.BytesRead)
		result, err = sys.LoadMazFromPages(filename, uintptr(lf.StartVA), lf.BytesRead)
		// Free the pre-loaded pages (ELF segments were copied to new pages by kernel)
		syscall.RawSyscall6(syscall.SYS_MUNMAP, uintptr(lf.StartVA),
			uintptr(lf.NumPages)*4096, 0, 0, 0, 0)
	} else {
		// Fallback: kernel reads from disk directly (bootstrap path)
		result, err = sys.LoadMaz(filename)
	}

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
