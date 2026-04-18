package mazdl

import (
	"strings"
	"sync"
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
