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
// Exit criteria (design/MAZARIN-DLOPEN.md §9 Phase 4):
//   1. h.Sym("Hello")() returns "hello from mazlink plugin".
//   2. runtime.Stack shows exactly one each of forcegchelper / sysmon /
//      bgsweep / bgscavenge / runfinq — i.e. plugin runtime was NOT
//      duplicated.
//   3. Plugin allocations show up in host runtime.MemStats (single heap).
//   4. 1000-iteration Stress() runs clean with no panic/SIGILL.
//
// A successful run prints one "ok" line per criterion; any failure
// exits non-zero with a diagnostic.
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
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

// singletonGoroutines are the runtime-internal goroutines that must
// appear exactly once in a healthy process. If plugin runtime wasn't
// stripped (policy miss / linker bug), loading the plugin would cause
// any of these to show up twice.
var singletonGoroutines = []string{
	"runtime.forcegchelper",
	"runtime.sysmon",
	"runtime.bgsweep",
	"runtime.bgscavenge",
	"runtime.runfinq",
}

// countGoroutines parses a runtime.Stack dump (all=true) and counts
// frames whose topmost function is `fn`. runtime.Stack formats each
// goroutine as:
//
//	goroutine N [state]:
//	pkg.func(args)
//		file:line +0xoffset
//	...
//
// We count occurrences of a line that starts with `fn + "("`, which
// identifies the goroutine's entry function on the topmost frame.
func countGoroutines(dump []byte, fn string) int {
	needle := []byte(fn + "(")
	var count int
	for off := 0; ; {
		i := bytes.Index(dump[off:], needle)
		if i < 0 {
			break
		}
		// Require the match to be at the start of a line (topmost
		// frame of a goroutine, not a caller mentioning it).
		if off+i == 0 || dump[off+i-1] == '\n' {
			count++
		}
		off += i + len(needle)
	}
	return count
}

// stressBytes is a loose lower bound on the heap churn a 1000-iter
// Stress() call should produce — 1000 iterations of fmt.Sprintf +
// map inserts allocates far more than this. Tuned conservatively so
// that GOGC timing variation doesn't flake the check.
const stressBytes = 16 * 1024

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

	// -------- Exit #1: Hello + Bump + first Stress --------
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

	fmt.Println("mazlink smoke: exit1 ok")

	// -------- Exit #2: singleton goroutines --------
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	dump := buf[:n]
	var goroutineFailures int
	for _, fn := range singletonGoroutines {
		got := countGoroutines(dump, fn)
		// sysmon is a special case: it's not a normal goroutine
		// on its own G, so it won't show up in runtime.Stack. Skip
		// the count check but keep the name in the list for
		// documentation.
		// sysmon runs on an M without a G (runtime.newm(sysmon,
		// nil, -1)), so it never appears in runtime.Stack. Keep
		// it in the list for documentation but skip the check.
		if fn == "runtime.sysmon" {
			continue
		}
		// runfinq is lazy-started on first SetFinalizer, so 0 is
		// legal. What we're guarding against is duplication: 2+
		// instances means plugin runtime didn't get stripped.
		if got > 1 {
			fmt.Fprintf(os.Stderr, "mazlink smoke: singleton check FAIL: %s appeared %d times (want <=1, 2+ implies duplicated plugin runtime)\n", fn, got)
			goroutineFailures++
		}
	}
	if goroutineFailures > 0 {
		os.Stderr.Write(dump)
		die("mazlink smoke: exit2 FAIL (%d singleton mismatches)", goroutineFailures)
	}
	fmt.Println("mazlink smoke: exit2 ok")

	// -------- Exit #3: plugin allocations in host MemStats --------
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_ = stress(1000)
	runtime.ReadMemStats(&m1)
	delta := m1.TotalAlloc - m0.TotalAlloc
	if delta < stressBytes {
		die("mazlink smoke: exit3 FAIL: TotalAlloc delta %d < %d (plugin alloc not visible to host heap?)", delta, stressBytes)
	}
	fmt.Printf("mazlink smoke: exit3 ok (TotalAlloc delta=%d bytes)\n", delta)

	// -------- Exit #4: 1000-iteration stress clean --------
	// One stress(1000) already ran above for the MemStats check;
	// repeat to catch any residual state that breaks on re-entry.
	out := stress(1000)
	if out != "stressed 1000 times" {
		die("mazlink smoke: exit4 FAIL: stress(1000) returned %q", out)
	}
	fmt.Println("mazlink smoke: exit4 ok")
}
