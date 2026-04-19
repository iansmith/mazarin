//go:build mazhost

package mazdl

import (
	"mazzy/mazarin/sys"
)

// RegisterHost populates the host (shepherd) module entry from the
// kernel-exposed symbol table. The kernel already parsed the
// shepherd's ELF at launch (kmazarin/ksyscall/launch.go buildSymbolTable)
// and cached every FUNC/OBJECT symbol's absolute VA in
// proc.Shepherd.SymbolTable. SysGetOwnExports serializes that map
// into userspace; mazdl just walks it and publishes each entry.
//
// Shepherds are ET_EXEC at fixed addresses, so every address in the
// returned table is already absolute — there is no load-base math
// (unlike the PIE Linux variant that walks /proc/self/maps).
//
// Must be called before any mazdl.Open. Idempotent.
func RegisterHost() (*Handle, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()

	if existing, ok := modules[hostSoname]; ok {
		return existing, nil
	}

	exports, err := sys.GetOwnExports()
	if err != nil {
		return nil, errorf("RegisterHost", "", "", "GetOwnExports: %v", err)
	}

	h := &Handle{
		soname:  hostSoname,
		base:    0,
		exports: make(map[string]uintptr, len(exports)),
	}
	for _, e := range exports {
		h.exports[e.Name] = e.Addr
	}

	modules[hostSoname] = h
	for name, addr := range h.exports {
		globalSyms[name] = symEntry{addr: addr, module: h}
	}
	return h, nil
}
