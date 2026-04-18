// Phase 3 host-probe.
//
// A trivial Go program whose only purpose is to give the mazlink linker
// something to link with `-dlopen-host-exports=<policy>`. After linking
// we inspect the result with readelf --dyn-syms and check that policy-
// matched symbols (runtime.mallocgc, runtime.newobject, ...) appear as
// GLOBAL DEFAULT FUNC in the output binary's dynsym table. That is the
// Phase 3 exit criterion in design/MAZARIN-DLOPEN.md §9.
//
// The binary itself doesn't need to do anything interesting — we only
// care about its .dynsym. But touching a map + printing makes sure the
// Go linker actually pulls in the runtime allocator symbols we want to
// see exported, rather than having deadcode elide them.
package main

import "fmt"

func main() {
	m := make(map[string]int, 4)
	for i := 0; i < 4; i++ {
		m[fmt.Sprintf("k%d", i)] = i * i
	}
	fmt.Printf("host-probe: %v\n", m)
}
