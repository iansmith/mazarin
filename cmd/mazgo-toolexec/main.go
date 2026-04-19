// Command mazgo-toolexec is a Go `-toolexec` wrapper that substitutes a
// caller-supplied mazlink binary for the stock `link` tool. All other tool
// invocations (compile, asm, cgo, etc.) pass through unchanged.
//
// Usage:
//
//	go build -toolexec="/path/to/mazgo-toolexec /path/to/mazlink" ...
//
// The -toolexec mechanism is documented in `go help build`: the named
// command is invoked as
//
//	<cmd> <tool> <tool-args...>
//
// for every tool the `go` driver would run. We inspect <tool>; if it's
// link (by basename), we replace it with the mazlink path and exec. Every
// other tool is exec'd unchanged.
//
// The wrapper is its own standalone binary (not a `go tool`) so that it can
// be invoked as a single exec with no `go tool` wrapping overhead.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "mazgo-toolexec: usage: mazgo-toolexec <mazlink-path> <tool> [args...]")
		os.Exit(2)
	}
	mazlink := os.Args[1]
	tool := os.Args[2]
	rest := os.Args[3:]

	target := tool
	// Preserve the original tool name as argv[0] even when we're redirecting
	// to mazlink. cmd/go parses `link -V=full` output as
	//   "<argv0> version <goversion> ..."
	// so if argv[0] is literally "/path/to/mazlink" the version line reads
	// "mazlink version ..." and cmd/go refuses it. Keeping argv[0] = tool
	// ("/.../link") makes the output parseable. mazlink's own logs pick
	// up argv[0] for identification — acceptable side effect.
	argv0 := tool
	if filepath.Base(tool) == "link" {
		target = mazlink
	}
	argv := append([]string{argv0}, rest...)
	if err := syscall.Exec(target, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "mazgo-toolexec: exec %s: %v\n", target, err)
		os.Exit(1)
	}
}
