// Mazlink smoke-test host.
//
// Opens a plugin built by mazlink, looks up its exported symbols, and
// exercises them. Stdlib "plugin" relies on dlopen, so the host itself
// is built with CGO_ENABLED=1 and stock Go.
//
// A successful run prints:
//
//	mazlink smoke: hello from mazlink plugin
//	mazlink smoke: bump=1 bump=2
//	mazlink smoke: stress=k000=0 k001=1 k002=4 ... | type=map[string]int keyKind=string
//
// and exits 0.
package main

import (
	"fmt"
	"os"
	"plugin"
)

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func lookup[T any](p *plugin.Plugin, name string) T {
	sym, err := p.Lookup(name)
	if err != nil {
		die("plugin.Lookup(%s): %v", name, err)
	}
	fn, ok := sym.(T)
	if !ok {
		die("%s has unexpected type: %T", name, sym)
	}
	return fn
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <plugin.maz>\n", os.Args[0])
		os.Exit(2)
	}
	path := os.Args[1]

	p, err := plugin.Open(path)
	if err != nil {
		die("plugin.Open(%q): %v", path, err)
	}

	hello := lookup[func() string](p, "Hello")
	fmt.Printf("mazlink smoke: %s\n", hello())

	bump := lookup[func() int](p, "Bump")
	fmt.Printf("mazlink smoke: bump=%d bump=%d\n", bump(), bump())

	stress := lookup[func(int) string](p, "Stress")
	fmt.Printf("mazlink smoke: stress=%s\n", stress(8))
}
