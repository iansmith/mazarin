package mazdl

import (
	"strings"
	"sync"
	"unsafe"
)

// Handle is an opaque reference to a loaded .maz module. A Handle is
// returned by Open and is the sole means by which callers reach a
// plugin's exported symbols (via Sym). Handles for the host itself are
// created by RegisterHost and stored in the global module table, but are
// not directly exposed to callers.
type Handle struct {
	// soname is the DT_SONAME the plugin declared, or the filename if
	// none was set. The host's soname is the sentinel "mazarin-host".
	soname string

	// base is the load address of the first PT_LOAD segment. All
	// exported addresses and relocations are computed relative to this.
	base uintptr

	// exports maps symbol name → absolute address. Populated during
	// Open from the plugin's DEFINED dynsym entries. For the host
	// Handle, populated by RegisterHost from the host's own
	// readable-via-self dynsym.
	exports map[string]uintptr

	// segments holds each mapped PT_LOAD's base/end/perms so Close can
	// munmap them. Unused in MVP (Close is a no-op) but recorded for
	// future cleanup.
	segments []segment
}

type segment struct {
	base  uintptr
	len   uintptr
	perms uint8 // 1=R 2=W 4=X bitfield
}

// Name returns the SONAME under which the module is registered in the
// global table. For plugins without an explicit SONAME, this is the
// filename passed to Open. For the host Handle it is "mazarin-host".
func (h *Handle) Name() string {
	return h.soname
}

// Sym looks up a symbol the plugin DEFINED in its dynsym and returns
// the resolved absolute address (base + symbol value). Unknown names
// return (0, err). Sym deliberately does not search other modules'
// exports — cross-module lookup is out of scope for MVP.
//
// Lookup tries the caller-supplied name verbatim first. If that misses,
// it falls back to a suffix search: Go's linker exports plugin symbols
// as "<pluginpath>.<Name>" (e.g. "mazlink.smoke/plugin.Hello") while
// stdlib's plugin.Lookup accepts just "Hello" and prepends the path
// internally. mazdl replicates that ergonomics by matching any export
// that ends in "."+name, provided the match is unique.
func (h *Handle) Sym(name string) (uintptr, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	if addr, ok := h.exports[name]; ok {
		return addr, nil
	}
	var matchName string
	var matchAddr uintptr
	var matchCount int
	suffix := "." + name
	for k, v := range h.exports {
		if strings.HasSuffix(k, suffix) {
			matchName = k
			matchAddr = v
			matchCount++
		}
	}
	switch matchCount {
	case 0:
		return 0, &mazdlError{op: "Sym", name: name, msg: "symbol not found in module " + h.soname}
	case 1:
		_ = matchName
		return matchAddr, nil
	default:
		return 0, &mazdlError{op: "Sym", name: name, msg: "ambiguous short name in module " + h.soname}
	}
}

// Close is a no-op in MVP. A full implementation would munmap the
// segments and remove the module from the global table; that requires
// reference-counting cross-module references and running finalizers,
// which is Phase 5+ work.
func (h *Handle) Close() error {
	return nil
}

// Funcval converts a function pointer (resolved from a plugin's dynsym) into
// a callable Go function. T must be a func type. Uses the standard Go funcval
// layout (first word = entry PC), matching the compiler's ABI.
func Funcval[T any](addr uintptr) T {
	if addr == 0 {
		var zero T
		return zero
	}
	type funcval struct{ fn uintptr }
	fv := &funcval{fn: addr}
	return *(*T)(unsafe.Pointer(&fv))
}

// modulesMu guards both modules and globalSyms. Open is fully serialized
// under this mutex; Sym takes it briefly for the map lookup.
var modulesMu sync.Mutex

// modules maps SONAME → Handle for every module currently loaded,
// including the host itself (keyed "mazarin-host"). Populated by
// RegisterHost and Open.
var modules = map[string]*Handle{}

// globalSyms is the flat, cross-module symbol table used only during
// Open's UNDEF resolution pass. It is keyed by symbol name; each entry
// records the defining module and the resolved absolute address.
// Publishing to this map is the final step of Open, after relocations
// have completed.
var globalSyms = map[string]symEntry{}

type symEntry struct {
	addr   uintptr
	module *Handle
}

// resolveGlobal looks up an UND symbol name in the global table, trying
// both the plain name and the ".abi0"-suffixed variant in either
// direction. Background:
//
//   - mazlink's host-export pass publishes the ABI0 copy of a function
//     under name+".abi0" when an ABIInternal counterpart is also
//     present, to avoid dynsym name collisions.
//   - mazlink's plugin-side rewrite tags a plugin UND with ".abi0" when
//     the reloc targets the ABI0 body (e.g. stack-based calls into
//     assembly-declared functions like internal/runtime/syscall/linux.
//     Syscall6), so PLT/JUMP_SLOT binds to the host's ABI0 entry point.
//
// The two cases don't always agree on which spelling is exported:
//
//   1. Plain-name plugin UND + host exports ABIInternal at plain name
//      → direct hit.
//   2. ".abi0"-tagged plugin UND + host has both ABIs and so exports
//      name+".abi0" → direct hit.
//   3. Plain-name plugin UND + host's ABIInternal was deadcoded, so only
//      name+".abi0" exists → fall through to the ".abi0" lookup.
//   4. ".abi0"-tagged plugin UND + host has ABI0 only, published at the
//      plain name → fall through to the name-without-".abi0" lookup.
//
// All four paths converge on the correct symbol without the host having
// to emit duplicate aliases. See
// mazlink-patches/cmd/link/internal/ld/mazdl.go for the matching
// emission logic.
func resolveGlobal(name string) (symEntry, bool) {
	if e, ok := globalSyms[name]; ok {
		return e, true
	}
	if e, ok := globalSyms[name+".abi0"]; ok {
		return e, true
	}
	if stripped, found := strings.CutSuffix(name, ".abi0"); found {
		if e, ok := globalSyms[stripped]; ok {
			return e, true
		}
	}
	return symEntry{}, false
}

// hostSoname is the sentinel DT_NEEDED token stamped into every plugin by
// mazlink's Phase-2 plugin mode. It is not a filesystem path — mazdl
// rejects any plugin whose DT_NEEDED list is not exactly [hostSoname].
const hostSoname = "mazarin-host"
