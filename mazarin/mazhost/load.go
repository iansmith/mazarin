// Package mazhost provides host-side support for loading .maz modules
// into a shepherd's address space. It wraps mazdl.OpenBytes, looks up
// MazarinMain/MazarinShepherd via the plugin's dynsym, and returns a
// callable func() built from a funcval.
//
// Build tag rationale: no `mazhost` tag here. The package is imported
// by both the generic shepherd (which links mazhost in and owns the
// resident copy — see dlopen-host-packages.txt) AND by plugins that
// want to spawn further plugins (rachel loads fontsvc, fs loads disk,
// etc.). In plugin builds the symbols compile but resolve through GOT
// to the shepherd's resident copy at load time, so the single
// modules/globalSyms registry stays consistent across the process.
package mazhost

import (
	"fmt"
	"runtime"
	"unsafe"

	merror "mazzy/mazarin/error"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazdl"
)

// LoadMazBootstrap loads a .maz module and returns (MazarinMain, MazarinShepherd-addr).
// The fc must already be connected. The shepherd argument is unused (historical API);
// the caller is responsible for invoking MazarinShepherd with its own injector by
// constructing a funcval from the returned second return value.
//
// On success, returns a func() that the caller should run as a goroutine.
func LoadMazBootstrap(fc *fsclient.Client, filename string, _ interface{}) (func(), uintptr, *merror.Error) {
	return loadMazInternal(fc, filename)
}

// LaunchMaz loads a .maz module by name and runs its MazarinMain in a new
// goroutine with a pre-grown stack. fc must already be connected.
func LaunchMaz(fc *fsclient.Client, name string) {
	path := "/" + name + ".maz"
	mazMain, _, err := loadMazInternal(fc, path)
	if err != nil {
		panic(fmt.Sprintf("[mazhost] LaunchMaz(%q) failed: %v", name, err))
	}
	go runWithLargeStack(mazMain)
}

// RunMaz runs an already-loaded .maz func on a pre-grown stack.
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

// loadMazInternal is the private implementation shared by LoadMazBootstrap and
// LaunchMaz. Flow:
//
//  1. Ensure RegisterHost has been called (idempotent).
//  2. Read plugin bytes via fsclient.ReadFile.
//  3. Hand them to mazdl.OpenBytes, which maps segments, applies
//     relocations, runs DT_INIT_ARRAY (moduledata registration), and
//     publishes DEFINED exports.
//  4. Look up MazarinMain (required) and MazarinShepherd (optional).
//  5. Build funcvals and return.
func loadMazInternal(fc *fsclient.Client, filename string) (func(), uintptr, *merror.Error) {
	if _, err := mazdl.RegisterHost(); err != nil {
		return nil, 0, merror.ErrInvalidELF.Wrap(fmt.Sprintf("%s: RegisterHost: %v", filename, err))
	}

	data, err := fc.ReadFile(filename)
	if err != nil {
		return nil, 0, merror.ErrFileNotFound.Wrap(fmt.Sprintf("%s: %v", filename, err))
	}
	if len(data) == 0 {
		return nil, 0, merror.ErrFileNotFound.Wrap(fmt.Sprintf("%s: ReadFile returned 0 bytes", filename))
	}

	h, err := mazdl.OpenBytes(filename, data)
	if err != nil {
		return nil, 0, merror.ErrInvalidELF.Wrap(fmt.Sprintf("%s: OpenBytes: %v", filename, err))
	}

	entryAddr, err := h.Sym("MazarinMain")
	if err != nil {
		return nil, 0, merror.ErrNoSymbol.Wrap(fmt.Sprintf("%s: MazarinMain: %v", filename, err))
	}

	// MazarinShepherd is optional — plugins without injection (e.g. prefs, helloworld)
	// don't define it. h.Sym returns an error in that case and we leave shepherdAddr==0.
	var shepherdAddr uintptr
	if addr, sErr := h.Sym("MazarinShepherd"); sErr == nil {
		shepherdAddr = addr
	}

	type funcval struct{ fn uintptr }
	fv := &funcval{fn: entryAddr}
	mazMain := *(*func())(unsafe.Pointer(&fv))

	return mazMain, shepherdAddr, nil
}
