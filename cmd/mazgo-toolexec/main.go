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
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// linkVersionFull handles `link -V=full` on mazlink's behalf. cmd/go uses
// that line as the link tool's action-cache ID, and for a release version
// string ("link version go1.26.4") it takes the line verbatim — so two
// different mazlink builds are indistinguishable and go silently reuses
// .maz outputs linked by a stale mazlink. Append a content hash of the
// mazlink binary as an extra field; cmd/go accepts trailing fields on a
// release line and caches on the whole line, so any mazlink rebuild now
// invalidates prior link outputs.
func linkVersionFull(mazlink, argv0 string) {
	out, err := exec.Command(mazlink, "-V=full").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mazgo-toolexec: %s -V=full: %v\n", mazlink, err)
		os.Exit(1)
	}
	f, err := os.Open(mazlink)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mazgo-toolexec: %v\n", err)
		os.Exit(1)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		fmt.Fprintf(os.Stderr, "mazgo-toolexec: hashing %s: %v\n", mazlink, err)
		os.Exit(1)
	}
	f.Close()
	line := strings.TrimSpace(string(out))
	// mazlink prints "<its argv0 basename> version ..."; rewrite the tool
	// name to match what cmd/go expects ("link").
	if i := strings.Index(line, " version "); i >= 0 {
		line = filepath.Base(argv0) + line[i:]
	}
	fmt.Printf("%s mazlink:%x\n", line, h.Sum(nil)[:8])
	os.Exit(0)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "mazgo-toolexec: usage: mazgo-toolexec <mazlink-path> <tool> [args...]")
		os.Exit(2)
	}
	mazlink := os.Args[1]
	tool := os.Args[2]
	rest := os.Args[3:]

	if filepath.Base(tool) == "link" && len(rest) == 1 && rest[0] == "-V=full" {
		linkVersionFull(mazlink, tool)
	}

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
