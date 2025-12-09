package dummy

import _ "unsafe"

// Link to main package symbols so the linker retains them even if only referenced from assembly.

//go:linkname kernelMain main.KernelMain
func kernelMain(r0, r1, atags uint32)

//go:linkname goEventLoopEntry main.GoEventLoopEntry
func goEventLoopEntry()

//go:linkname irqHandler main.IRQHandler
func irqHandler()

//go:linkname fiqHandler main.FIQHandler
func fiqHandler()

//go:linkname sErrorHandler main.SErrorHandler
func sErrorHandler()

//go:linkname growStackForCurrent main.GrowStackForCurrent
func growStackForCurrent()

// GoEventLoopEntryFunc returns the Go event loop entry point so that callers
// can keep the symbol reachable even if only assembly references it.
func GoEventLoopEntryFunc() func() {
	return goEventLoopEntry
}

var keepers = []interface{}{
	kernelMain,
	goEventLoopEntry,
	irqHandler,
	fiqHandler,
	sErrorHandler,
	growStackForCurrent,
}
