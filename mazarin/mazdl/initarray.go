package mazdl

import (
	"encoding/binary"
	"unsafe"
)

// runInitArray walks DT_INIT_ARRAY entries and calls each one. The
// linker guarantees the first entry is an addmoduledata wrapper —
// registering the plugin's firstmoduledata with the host runtime — and
// subsequent entries are user init funcs (package init chains,
// go:linkname hooks, etc.).
//
// Each init-array entry is a function pointer written by the linker as
// a plugin-local vaddr. Go's external linker emits R_*_RELATIVE against
// these slots so applyRelative patches them to absolute addresses
// before we get here; Go's internal linker (what mazgo+mazlink use)
// does NOT emit those relocations — the slots still contain raw
// vaddrs. We detect the two cases by magnitude: any value less than
// relocBase is a raw vaddr and needs `+= relocBase`, anything larger
// is already a real address we leave alone. relocBase is on the order
// of the mmap base (multi-GB), so a plugin-local vaddr (tens of
// megabytes at most) is trivially distinguishable.
//
// If any init function panics, the panic propagates up — mazdl does
// not attempt partial rollback. A failing init means the plugin is
// broken in a way the host can't recover from, so crashing the caller
// is the honest outcome.
func runInitArray(relocBase uintptr, initArrayBytes []byte) {
	for off := 0; off+8 <= len(initArrayBytes); off += 8 {
		fn := uintptr(binary.LittleEndian.Uint64(initArrayBytes[off:]))
		if fn == 0 {
			continue
		}
		if fn < relocBase {
			fn += relocBase
		}
		callInitFunc(fn)
	}
}

// callInitFunc is the isolated call site so the Go compiler doesn't
// inline it into runInitArray. We construct a funcval pointing at the
// init-array entry and invoke it through a func() variable — the same
// pattern mazhost/load.go uses to call a plugin's MazarinMain.
//
//go:nosplit
func callInitFunc(fn uintptr) {
	fv := &funcval{fn: fn}
	f := *(*func())(unsafe.Pointer(&fv))
	f()
}

// funcval mirrors Go's internal funcval layout (cmd/compile/internal/
// abi.FuncPcABIInternal). A *funcval in Go's ABI is a pointer to a
// struct whose first word is the entry PC; the compiler dereferences
// that to produce a direct call.
type funcval struct {
	fn uintptr
}
