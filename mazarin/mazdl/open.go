package mazdl

import (
	"debug/elf"
	"errors"
	"strings"
	"unsafe"
)

// Open loads the .maz plugin at filename into the current address space
// and returns a Handle through which the caller can reach the plugin's
// exported symbols. Open is fully serialized — two concurrent loads
// block each other — because globalSyms publication must be atomic with
// respect to another load's UNDEF resolution.
//
// Pipeline (each step gated by the previous succeeding):
//
//  1. elf.Open the file. Reject anything that isn't ET_DYN (Phase 2
//     plugins are shared-object shape).
//  2. Reserve one anon VA range large enough for every PT_LOAD.
//  3. Copy each PT_LOAD's file bytes into that region at the segment's
//     p_vaddr offset.
//  4. Apply R_*_RELATIVE. After this step intra-module pointers are
//     valid.
//  5. Verify DT_NEEDED is exactly ["mazarin-host"] — anything else
//     means the plugin was built for a different host ABI.
//  6. Resolve UNDEF relocations (.rela.dyn + .rela.plt) by looking up
//     each referenced name in globalSyms.
//  7. Flip per-segment page permissions to what the phdr p_flags say
//     (R, RX, RW). Any slot we need to patch later is covered by the
//     RW segment; executable regions become RX now.
//  8. Run DT_INIT_ARRAY (first entry = linker's addmoduledata wrapper,
//     remainder = user inits).
//  9. Publish the plugin's DEFINED dynsym to globalSyms under its
//     SONAME (or filename if no SONAME set).
//
// On any step's failure the partial state is left in place — MVP does
// not roll back; the caller should treat a failed Open as a terminal
// condition and exit.
func Open(filename string) (*Handle, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()

	if _, ok := modules[hostSoname]; !ok {
		return nil, errorf("Open", filename, "", "RegisterHost must be called before Open")
	}

	f, err := elf.Open(filename)
	if err != nil {
		return nil, errorf("Open", filename, "", "elf.Open: %v", err)
	}
	defer f.Close()

	if f.Type != elf.ET_DYN {
		return nil, errorf("Open", filename, "", "expected ET_DYN, got %v", f.Type)
	}

	// Compute the span: from the lowest PT_LOAD p_vaddr to the highest
	// p_vaddr + p_memsz. We reserve that much anon memory and copy
	// each segment to base + (seg.vaddr - loLoad).
	var loads []*elf.Prog
	var loLoad, hiLoad uint64
	loLoad = ^uint64(0)
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		loads = append(loads, p)
		if p.Vaddr < loLoad {
			loLoad = p.Vaddr
		}
		end := p.Vaddr + p.Memsz
		if end > hiLoad {
			hiLoad = end
		}
	}
	if len(loads) == 0 {
		return nil, errorf("Open", filename, "", "no PT_LOAD segments")
	}
	span := uintptr(hiLoad - loLoad)

	base, err := reserveAndMap(span)
	if err != nil {
		return nil, err
	}
	// The effective load base for relocation arithmetic is
	// `base - loLoad` so that (base + ph.vaddr) lands at the right
	// place for non-zero-based layouts.
	relocBase := base - uintptr(loLoad)

	// Copy each segment's file bytes into place.
	for _, p := range loads {
		dst := relocBase + uintptr(p.Vaddr)
		if p.Filesz > 0 {
			data := make([]byte, p.Filesz)
			if _, err := p.ReadAt(data, 0); err != nil {
				return nil, errorf("Open", filename, "", "read PT_LOAD at vaddr 0x%x: %v", p.Vaddr, err)
			}
			dstSlice := unsafe.Slice((*byte)(unsafe.Pointer(dst)), p.Filesz)
			copy(dstSlice, data)
		}
		// p.Memsz > p.Filesz means bss-style zero-fill; the anon
		// mapping is already zeroed so nothing to do.
	}

	h := &Handle{
		soname:  elfSoname(f, filename),
		base:    relocBase,
		exports: map[string]uintptr{},
	}

	// Go's internal-linked ET_DYN emits the dynamic-relocation section as
	// ".rela" (no .dyn suffix). Stock cgo-linked objects use ".rela.dyn".
	// Try both; whichever is non-nil is the one we walk for RELATIVE and
	// non-JUMP_SLOT symbol relocations.
	relaDyn, err := sectionBytes(f, ".rela.dyn")
	if err != nil {
		return nil, err
	}
	if len(relaDyn) == 0 {
		relaDyn, err = sectionBytes(f, ".rela")
		if err != nil {
			return nil, err
		}
	}
	relaPlt, err := sectionBytes(f, ".rela.plt")
	if err != nil {
		return nil, err
	}

	// Step 4: RELATIVE relocs (self-contained, no symbol lookup).
	if err := applyRelative(relocBase, relaDyn); err != nil {
		return nil, err
	}

	// Step 5: DT_NEEDED check. debug/elf exposes this via ImportedLibraries.
	libs, err := f.ImportedLibraries()
	if err != nil {
		return nil, errorf("Open", filename, "", "ImportedLibraries: %v", err)
	}
	if len(libs) != 1 || libs[0] != hostSoname {
		return nil, errorf("Open", filename, "", "DT_NEEDED must be exactly [%q], got %v", hostSoname, libs)
	}

	// Step 6: UNDEF resolution.
	dynsyms, err := f.DynamicSymbols()
	if err != nil {
		return nil, errorf("Open", filename, "", "DynamicSymbols: %v", err)
	}
	if err := applySymbolRelocs(relocBase, relaDyn, dynsyms); err != nil {
		return nil, err
	}
	if err := applySymbolRelocs(relocBase, relaPlt, dynsyms); err != nil {
		return nil, err
	}

	// Rewrite runtime funcvals that mazlink emitted with a dead RELATIVE
	// reloc into the zero-padding gap at the end of the plugin's .text.
	// See memory/mazlink_funcval_dead_reloc_bug.md and
	// design/MAZDL-PHASE4-CONTINUATION.md. Option B: loader-side patch
	// until mazlink option A lands.
	if err := rewriteHostFuncvals(f, relocBase); err != nil {
		return nil, errorf("Open", filename, "", "rewriteHostFuncvals: %v", err)
	}

	// Step 7: per-segment permission flip. We compute each segment's
	// range within our anon reservation and mprotect to the phdr's
	// p_flags value. The first mapping was PROT_READ|PROT_WRITE so we
	// could copy + relocate; now we tighten.
	for _, p := range loads {
		segBase := relocBase + uintptr(p.Vaddr)
		segLen := uintptr(p.Memsz)
		perms := phdrPerms(p.Flags)
		h.segments = append(h.segments, segment{base: segBase, len: segLen, perms: perms})
		if err := mprotectSegment(pageDown(segBase), pageUp(segLen+(segBase-pageDown(segBase))), perms); err != nil {
			return nil, err
		}
	}

	// Step 8a: DT_INIT_ARRAY. We find it by section name (".init_array")
	// because debug/elf doesn't expose DT_INIT_ARRAY via its public API;
	// ET_DYN Go plugins always emit a .init_array section at the same
	// location, so name-based lookup is reliable here. The only entry
	// Go emits in .init_array is the addmoduledata wrapper — which
	// registers the plugin's firstmoduledata with the host runtime but
	// does NOT run any Go-level init funcs.
	if ia, err := sectionBytes(f, ".init_array"); err == nil && len(ia) > 0 {
		runInitArray(relocBase, ia)
	}

	// Step 8b: run the plugin's package-init chain. plugin_lastmoduleinit
	// walks the moduledata chain for the newest entry (the one we just
	// registered) and returns its initTasks; runtime.doInit runs them.
	// Mirrors the two-step sequence in src/plugin/plugin_dlopen.go.
	if err := runPluginInitTasks(filename); err != nil {
		return nil, err
	}

	// Step 9: publish DEFINED exports.
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
		addr := relocBase + uintptr(s.Value)
		h.exports[s.Name] = addr
		// Duplicate-name handling: the plugin's dynsym includes its own
		// copies of compiler-generated type descriptors (type:uint8,
		// type:int, itabs, etc.) that the host has already published from
		// its -dlopen-host-exports policy. The plugin's own GLOB_DAT for
		// those symbols resolves intra-module (see applySymbolRelocs) so
		// we don't need to overwrite globalSyms. Skipping the duplicate
		// keeps the host as the canonical owner and prevents a later
		// plugin from binding against this plugin's type copies. h.Sym
		// still works because we've already written h.exports above.
		if _, dup := globalSyms[s.Name]; !dup {
			globalSyms[s.Name] = symEntry{addr: addr, module: h}
		}
	}

	modules[h.soname] = h

	return h, nil
}

// sectionBytes returns the raw bytes of a named section, or an empty
// slice if the section isn't present (e.g., plugin with no .rela.plt).
func sectionBytes(f *elf.File, name string) ([]byte, error) {
	s := f.Section(name)
	if s == nil {
		return nil, nil
	}
	return s.Data()
}

// elfSoname returns the plugin's DT_SONAME if it declared one, or the
// filename as a fallback. debug/elf doesn't expose DT_SONAME directly,
// so MVP always uses the filename — SONAME support is a follow-up.
func elfSoname(_ *elf.File, filename string) string {
	return filename
}

// phdrPerms converts an ELF p_flags value (PF_R/PF_W/PF_X bits) into
// the mmap_linux.go permission bitfield (segPermR/W/X).
func phdrPerms(flags elf.ProgFlag) uint8 {
	var p uint8
	if flags&elf.PF_R != 0 {
		p |= segPermR
	}
	if flags&elf.PF_W != 0 {
		p |= segPermW
	}
	if flags&elf.PF_X != 0 {
		p |= segPermX
	}
	return p
}

const pageSize = 4096

func pageDown(v uintptr) uintptr { return v &^ (pageSize - 1) }
func pageUp(v uintptr) uintptr   { return (v + pageSize - 1) &^ (pageSize - 1) }

// rewriteHostFuncvals walks the plugin's full symbol table looking for
// Go funcvals — 8-byte STT_OBJECT symbols whose name ends in "·f" (the
// compiler-generated funcval naming convention, U+00B7 middle dot +
// lowercase f). For each one whose underlying function (the name minus
// "·f") is exported by the host, it overwrites the .fn field with the
// host function's address.
//
// Why this exists: when mazlink strips a host-policy package's code
// from the plugin, it leaves the per-function funcval objects behind in
// .data.rel.ro with an R_AARCH64_RELATIVE reloc whose addend points at
// the placeholder plugin address where the stripped function used to
// live — i.e. the zero-filled padding between the last real plugin
// .text function and runtime.etext. Any indirect call through that
// funcval (e.g. a map type's Hasher, read inside runtime.mapassign_faststr)
// branches into the padding and SIGILLs on `udf #0`.
//
// The correct fix is in mazlink (option A: emit GLOB_DAT/ABS64 against
// the host's UNDEF host-policy symbol instead of RELATIVE). This loader
// patch is option B — a localized workaround that unblocks Phase 4 while
// option A is designed and landed.
func rewriteHostFuncvals(f *elf.File, relocBase uintptr) error {
	symtab, err := f.Symbols()
	if err != nil {
		if errors.Is(err, elf.ErrNoSymbols) {
			return nil
		}
		return err
	}
	const fvSuffix = "\u00b7f"
	for _, s := range symtab {
		if elf.ST_TYPE(s.Info) != elf.STT_OBJECT || s.Size != 8 {
			continue
		}
		if !strings.HasSuffix(s.Name, fvSuffix) {
			continue
		}
		hostName := strings.TrimSuffix(s.Name, fvSuffix)
		e, ok := globalSyms[hostName]
		if !ok {
			continue
		}
		*(*uint64)(unsafe.Pointer(relocBase + uintptr(s.Value))) = uint64(e.addr)
	}
	return nil
}
