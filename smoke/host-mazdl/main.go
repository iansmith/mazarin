// Phase 4 smoke-test host.
//
// The reference-track host (smoke/host/main.go) uses stdlib's plugin
// package, which relies on cgo + dlopen. This mazdl-track host replaces
// plugin.Open with mazdl.Open — same plugin binary, same exported
// symbols, but loaded through the Phase-4 userspace loader against a
// Phase-3 host dynsym.
//
// Build-time requirements:
//   - mazgo + mazlink (this binary must carry the policy symbols as
//     GLOBAL DEFAULT FUNC in .dynsym)
//   - CGO_ENABLED=0, -linkmode=internal, -dlopen-host-exports=<policy>
//
// A successful run prints (same as smoke/host):
//
//	mazlink smoke: hello from mazlink plugin
//	mazlink smoke: bump=1 bump=2
//	mazlink smoke: stress=stressed 8 times
//
// Divergence from smoke/host:
//   - Calls mazdl.RegisterHost() once at startup.
//   - Uses h.Sym("Hello") and reinterprets the resulting uintptr as a
//     function pointer (mazdl's API stays under the Go-plugin-sig
//     level — no generic type lookup).
package main

import (
	"fmt"
	"os"
	"unsafe"

	"mazzy/mazarin/mazdl"
)

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// funcPtr converts an address returned by mazdl Handle.Sym into a Go
// func of type T. T must be a func type matching the plugin symbol's
// signature; mazdl has no type information to validate this, so a
// mismatch is a run-time crash. Same escape hatch mazhost/load.go uses.
func funcPtr[T any](addr uintptr) T {
	fv := &struct{ fn uintptr }{fn: addr}
	return *(*T)(unsafe.Pointer(&fv))
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <plugin.maz>\n", os.Args[0])
		os.Exit(2)
	}
	path := os.Args[1]

	if _, err := mazdl.RegisterHost(); err != nil {
		die("mazdl.RegisterHost: %v", err)
	}

	h, err := mazdl.Open(path)
	if err != nil {
		die("mazdl.Open(%q): %v", path, err)
	}

	helloAddr, err := h.Sym("Hello")
	if err != nil {
		die("Sym(Hello): %v", err)
	}
	hello := funcPtr[func() string](helloAddr)
	fmt.Printf("mazlink smoke: %s\n", hello())

	bumpAddr, err := h.Sym("Bump")
	if err != nil {
		die("Sym(Bump): %v", err)
	}
	bump := funcPtr[func() int](bumpAddr)
	fmt.Printf("mazlink smoke: bump=%d bump=%d\n", bump(), bump())

	stressAddr, err := h.Sym("Stress")
	if err != nil {
		die("Sym(Stress): %v", err)
	}
	stress := funcPtr[func(int) string](stressAddr)
	fmt.Printf("mazlink smoke: stress=%s\n", stress(8))
}
