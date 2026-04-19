//go:build linux && !mazhost

package mazdl

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// hostLoadBase returns the runtime load base of the current executable
// by parsing /proc/self/maps. Go dynamically-linked binaries are PIE,
// so dynsym st_value is relative; we need to add this base to get the
// live address. For PIE, the first PT_LOAD has p_vaddr==0, so the
// start of the first segment mapping for exePath is the load base
// (adjusted by the minimum phdr vaddr in case that assumption ever
// breaks — today it's always zero).
func hostLoadBase(f *elf.File, exePath string) (uintptr, error) {
	var minVaddr uint64 = ^uint64(0)
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Vaddr < minVaddr {
			minVaddr = p.Vaddr
		}
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasSuffix(line, " "+exePath) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		dash := strings.SplitN(fields[0], "-", 2)
		start, err := strconv.ParseUint(dash[0], 16, 64)
		if err != nil {
			continue
		}
		return uintptr(start) - uintptr(minVaddr), nil
	}
	return 0, errorf("RegisterHost", exePath, "", "exe not found in /proc/self/maps")
}

// RegisterHost reads the current process's own binary, extracts every
// GLOBAL DEFINED FUNC/OBJECT entry from .dynsym (these are the symbols
// mazlink marked with -dlopen-host-exports), and publishes them to the
// global module table under the "mazarin-host" SONAME.
//
// Must be called before any mazdl.Open. Idempotent: a second call
// returns the existing host Handle.
//
// Go's dynamically-linked executables are PIE (internal-linked + musl
// interpreter), so dynsym st_value is a load-relative offset, not an
// absolute VA. We compute the host's runtime load base by comparing
// runtime.GC's live PC (reflect.ValueOf) to its dynsym offset; every
// exported symbol's runtime address is then `loadBase + st_value`.
func RegisterHost() (*Handle, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()

	if existing, ok := modules[hostSoname]; ok {
		return existing, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return nil, errorf("RegisterHost", "", "", "os.Executable: %v", err)
	}

	f, err := elf.Open(exePath)
	if err != nil {
		return nil, errorf("RegisterHost", exePath, "", "elf.Open: %v", err)
	}
	defer f.Close()

	dynsyms, err := f.DynamicSymbols()
	if err != nil {
		return nil, errorf("RegisterHost", exePath, "", "DynamicSymbols: %v", err)
	}

	loadBase, err := hostLoadBase(f, exePath)
	if err != nil {
		return nil, errorf("RegisterHost", exePath, "", "hostLoadBase: %v", err)
	}

	h := &Handle{
		soname:  hostSoname,
		base:    loadBase,
		exports: make(map[string]uintptr, len(dynsyms)),
	}

	for _, s := range dynsyms {
		if s.Section == elf.SHN_UNDEF {
			continue
		}
		if elf.ST_BIND(s.Info) != elf.STB_GLOBAL {
			continue
		}
		t := elf.ST_TYPE(s.Info)
		if t != elf.STT_FUNC && t != elf.STT_OBJECT {
			continue
		}
		if s.Value == 0 {
			continue
		}
		h.exports[s.Name] = loadBase + uintptr(s.Value)
	}

	modules[hostSoname] = h
	for name, addr := range h.exports {
		globalSyms[name] = symEntry{addr: addr, module: h}
	}
	return h, nil
}
