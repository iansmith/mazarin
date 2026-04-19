// Mazlink smoke-test plugin.
package main

import "fmt"

func Hello() string {
	return "hello from mazlink plugin"
}

var count int

func Bump() int {
	count++
	return count
}

// Stress exercises the exact code path that broke Phase 4 pre-fix:
// map[string]T operations route through runtime.mapassign_faststr,
// which dereferences maptype.Hasher (a *funcval) and calls through
// it — the indirection where host-policy funcvals with dead RELATIVE
// relocs would SIGILL. Doing n iterations gives exit criterion 4 a
// clean 1000-iter run and puts enough load on the host heap for
// exit criterion 3 (MemStats delta) to be unambiguous.
func Stress(n int) string {
	m := make(map[string]int, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%03d", i%997)
		m[k] = m[k] + i
	}
	var total int
	for _, v := range m {
		total += v
	}
	_ = total
	return fmt.Sprintf("stressed %d times", n)
}

func main() {}
