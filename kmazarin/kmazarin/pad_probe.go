package main

// padCounter is a global to prevent the compiler from eliminating pad calls.
var padCounter uint64

// padProbe adds a small amount of code to the calling function.
// The compiler cannot eliminate this because it has a visible side effect.
//
//go:noinline
func padProbe() {
	padCounter++
}

// padProbeStr takes a string argument (adds .rodata reference) but does no I/O.
//
//go:noinline
func padProbeStr(s string) {
	padCounter += uint64(len(s))
}
