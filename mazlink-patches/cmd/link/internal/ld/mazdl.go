// Copyright 2026 The Mazarin Authors.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Mazlink dlopen support.
//
// When -dlopen-host-packages=<policy> is passed, the linker converts every
// symbol owned by a policy-listed Go package into an UNDEF dynamic import
// (sym.SDYNIMPORT). Stock Go's linker infrastructure then routes:
//
//   - function call sites (R_CALL / R_CALLARM64) through .plt (via addpltsym),
//     which in turn emits R_*_JUMP_SLOT into .rela.plt and an UNDEF entry in
//     .dynsym.
//   - data references (R_GOTPCREL / R_ARM64_GOTPCREL) through .got (via
//     AddGotSym), which emits R_*_GLOB_DAT in .rela.dyn.
//   - .dynamic tags (DT_JMPREL, DT_PLTRELSZ, DT_PLTREL=DT_RELA, DT_PLTGOT)
//     get written by doelf()/elfdynhash() when the .rela.plt section is
//     non-empty.
//
// We also emit DT_NEEDED="mazarin-host" as a sanity label that mazdl.Open
// checks before binding. This file wires up the policy loader, matcher, and
// the symbol-rewrite pass that turns runtime/internal-runtime content into
// host imports. No ELF or architecture code lives here — that's all stock
// Go paths that activate when the symbol type is SDYNIMPORT.
//
// See design/MAZARIN-DLOPEN.md §2 (policy list), §3 (ABI contract), §4
// (linker changes). This is Phase 2.

package ld

import (
	"bufio"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"debug/elf"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Flags declared here so this single file carries the whole dlopen surface.
// flag.String registers against the default FlagSet at init, and the linker's
// main.go flag.Parse() picks it up the same as any flag declared in main.go.
//
// -dlopen-host-packages makes this build a **plugin** (policy-matched symbols
// are rewritten to SDYNIMPORT so they resolve against the host at load time).
// -dlopen-host-exports makes this build a **host** (policy-matched symbols
// that are DEFINED in this binary are marked GLOBAL DEFINED in .dynsym so
// plugins built with -dlopen-host-packages can bind against them).
//
// The two flags are mutually exclusive; one makes a plugin, the other a host.
var (
	flagDlopenHostPackages = flag.String("dlopen-host-packages", "",
		"mazlink: path to policy file listing Go packages resolved against the host at plugin load time (plugin mode)")
	flagDlopenHostExports = flag.String("dlopen-host-exports", "",
		"mazlink: path to policy file listing Go packages this binary exports to plugins (host mode)")
)

// The soname-ish string we stamp into DT_NEEDED. mazdl.Open refuses to bind a
// plugin whose DT_NEEDED isn't this exact token. There is no glibc "search
// path" behavior — this is just a sanity tag that says "I expect to be loaded
// into a Mazarin host and resolve runtime.* against it."
const mazdlHostTag = "mazarin-host"

// HostPolicy is the parsed form of dlopen-host-packages.txt.
//
// Two match modes:
//   - exact: "runtime" matches only package "runtime".
//   - prefix (trailing /...): "internal/runtime/..." matches "internal/runtime",
//     "internal/runtime/atomic", "internal/runtime/gc", etc.
//
// Matching is done on the Go import path (SymPkg()), not symbol name, so a
// reflection/type symbol like "type:runtime.mspan" stays in the plugin unless
// its owning package is itself on the list.
type HostPolicy struct {
	exact    map[string]bool // import path → true
	prefixes []string        // includes the "internal/runtime/" trailing slash; matched with HasPrefix
	rawLines []string        // for diagnostics
}

// loadHostPolicy parses a policy file. Format:
//
//	# comment line
//	runtime
//	internal/runtime/...
//
// Trailing "/..." means "this package and all subpackages." Leading/trailing
// whitespace is stripped. Blank lines and "#" comments are ignored.
func loadHostPolicy(path string) (*HostPolicy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mazlink: open %s: %w", path, err)
	}
	defer f.Close()

	p := &HostPolicy{exact: map[string]bool{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p.rawLines = append(p.rawLines, line)
		if strings.HasSuffix(line, "/...") {
			// Prefix match: store "foo/bar/" (with trailing slash) so "foo/bar"
			// itself matches via == on the bare form, and "foo/bar/baz" via
			// HasPrefix. We handle both below in Matches.
			base := strings.TrimSuffix(line, "/...")
			p.exact[base] = true
			p.prefixes = append(p.prefixes, base+"/")
		} else {
			p.exact[line] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mazlink: read %s: %w", path, err)
	}
	if len(p.exact) == 0 && len(p.prefixes) == 0 {
		return nil, fmt.Errorf("mazlink: policy file %s is empty", path)
	}
	return p, nil
}

// Matches reports whether importPath is owned by a policy-listed package.
// An empty path never matches (non-package symbols like compiler builtins).
func (p *HostPolicy) Matches(importPath string) bool {
	if importPath == "" || p == nil {
		return false
	}
	if p.exact[importPath] {
		return true
	}
	for _, pref := range p.prefixes {
		if strings.HasPrefix(importPath, pref) {
			return true
		}
	}
	return false
}

// rewriteHostSymsAsDynimport walks every loaded symbol; for each whose owning
// package matches the policy it:
//
//  1. flips SymType to SDYNIMPORT (turns DEFINED→UNDEF; stock Go's addpltsym,
//     AddGotSym, and Adddynsym all key off this),
//  2. drops the symbol's content bytes (the function body / data is no longer
//     the authoritative copy — the host's is),
//  3. zeroes its size, clears relocations (so deadcode doesn't drag other
//     runtime content into the plugin via a body that won't be emitted),
//  4. clears the outer/sub-symbol relationship (pclntab / funcdata pieces
//     keyed off the original function body would otherwise point into
//     now-orphaned sub-symbols),
//  5. stamps DynimpLib=mazarin-host + Extname=<name> so
//     elfadddynsym/elfdynhash produce the right .dynsym entry and
//     DT_NEEDED gets the correct soname association,
//  6. sets ELF symbol type (STT_FUNC vs STT_OBJECT) from the pre-rewrite
//     kind so the dynsym entry carries the right type bit — the arch's
//     reloc emission code (R_X86_64_PLT32 vs R_X86_64_GOTPCREL) ordinarily
//     looks at SymElfType.
//
// Must run after loadlib (so packages/syms are loaded) and before deadcode
// (so no orphan bodies survive the sweep). The loadlib hook at the end of
// (*Link).loadlib is the right place.
func rewriteHostSymsAsDynimport(ctxt *Link, p *HostPolicy) (converted int) {
	ldr := ctxt.loader
	for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
		pkg := ldr.SymPkg(i)
		if !p.Matches(pkg) {
			continue
		}
		// Filter: only convert symbols the plugin would otherwise carry as
		// content (text, data, ro-data, bss). Leave already-undef, already-
		// host-obj, or already-dynimport alone.
		st := ldr.SymType(i)
		if !hostPolicyRewritable(st) {
			continue
		}

		// Record the pre-rewrite kind for the ELF type hint below.
		wasText := st.IsText()

		sb := ldr.MakeSymbolUpdater(i)
		sb.SetType(sym.SDYNIMPORT)
		sb.SetData(nil)
		sb.SetSize(0)
		sb.ResetRelocs()
		// Sub-symbols (pclntab / funcdata slivers) whose outer is this sym
		// will become unreferenced once we drop the content above, and
		// deadcode will sweep them. Loader has no SetOuterSym to clear the
		// back-pointer explicitly; we rely on the reachability walk.
		// DT_NEEDED "mazarin-host" is a single string for every import; the
		// actual binding happens by name against the host's global export
		// table, not by library-file resolution. DynimpLib is still required
		// for elfdynhash to build .gnu.version_r entries consistently.
		ldr.SetSymDynimplib(i, mazdlHostTag)
		ldr.SetSymExtname(i, ldr.SymName(i))
		ldr.SetSymDynimpvers(i, "")
		if wasText {
			ldr.SetSymElfType(i, elf.STT_FUNC)
		} else {
			ldr.SetSymElfType(i, elf.STT_OBJECT)
		}
		converted++
	}
	// Queue a single DT_NEEDED "mazarin-host" entry for this binary. We can't
	// call adddynlib directly here: ctxt.Dynamic is populated later by
	// setArchSyms (after doelf), so adddynlib would panic on the null symbol.
	// Appending to dynlib (a package-level slice drained by addexport) matches
	// how stock cgo_import_dynamic stages libraries for later emission.
	dynlib = append(dynlib, mazdlHostTag)
	return converted
}

// hostPolicyRewritable reports whether a symbol's current kind is a sensible
// candidate for conversion to SDYNIMPORT. We deliberately skip kinds that
// either already are imports (SDYNIMPORT, SHOSTOBJ, SUNDEFEXT, SXREF) or
// that correspond to linker-generated metadata we don't own (Sxxx == 0, and
// the pclntab/funcdata kinds which are owner-keyed).
func hostPolicyRewritable(k sym.SymKind) bool {
	// IsText() covers STEXT..STEXTEND; IsData() covers SNOPTRDATA..SNOPTRBSS
	// (which includes SDATA, SRODATA, SBSS, STLSBSS, SGOFUNC, etc.). These are
	// the kinds whose content would otherwise be emitted into the plugin image.
	if k.IsText() || k.IsData() {
		return true
	}
	return false
}

// emitHostExportsDynsym is Phase 3 of design/MAZARIN-DLOPEN.md: walk every
// loaded symbol, and for each DEFINED symbol whose owning Go package is on
// the policy list, mark it as a dynamic cgo export. Stock Go's addexport()
// pass (called from asmb) walks ctxt.dynexp and hands each entry to
// Adddynsym, which in turn calls elfadddynsym — the result is a
// GLOBAL|DEFAULT dynsym entry with STT_FUNC (for text) or STT_OBJECT (for
// data), pointing at the symbol's in-binary address. Plugins built with
// -dlopen-host-packages contain matching UNDEF entries; at load time mazdl
// binds the two by name.
//
// We reuse the existing CgoExportDynamic machinery rather than writing a
// parallel emission path: it is exactly the "GLOBAL DEFINED in .dynsym"
// semantics we want, and it already handles ELF niceties (dynamic linking
// required, symtab sizing, STT_FUNC vs STT_OBJECT, reachability assert).
//
// Must run at the same place as rewriteHostSymsAsDynimport — end of
// loadlib — so all package symbols exist. Reachability is established
// later (deadcode); addexport checks AttrReachable when it drains dynexp.
// Anything on the policy list that the host doesn't actually reference
// would therefore be dropped by deadcode before export — which is fine,
// because a plugin that needs such a symbol could not have been built
// against this host anyway (the host lacks the code).
func emitHostExportsDynsym(ctxt *Link, p *HostPolicy) (exported int) {
	ldr := ctxt.loader
	// Force dynamic-linking mode on the output binary. Stock Go's linksetup
	// sets FlagD=true (suppressing .dynsym) when BuildMode=Exe and nothing
	// has imported a shared library. We are exporting dynsym without any
	// imports, so bump havedynamic to keep the dynamic-linking pipeline alive.
	havedynamic = 1

	for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
		pkg := ldr.SymPkg(i)
		if !p.Matches(pkg) {
			continue
		}
		st := ldr.SymType(i)
		// Export text + all data-like kinds. IsData() spans
		// SNOPTRDATA..SNOPTRBSS, covering SDATA, SINITARR, SBSS,
		// SNOPTRBSS — every ordinary writable/zero-init global. A prior
		// version used IsNOPTRDATA() which only covers SNOPTRDATA..
		// SNOPTRDATAEND and silently dropped BSS symbols like
		// runtime.writeBarrier, producing an "unresolved symbol
		// (R_*_GLOB_DAT)" at plugin load. IsRODATA catches read-only
		// vars in their separate kind range. Text is exported for the
		// obvious reason — plugin code calls it through JUMP_SLOT.
		if !st.IsText() && !st.IsData() && !st.IsRODATA() {
			continue
		}
		name := ldr.SymName(i)
		// Skip closures (function literals nested inside another function).
		// Their pcln aux syms are co-owned with the outer function; marking
		// them as independent dynamic exports roots them in deadcode but
		// pclntab generation later trips over partially-resolved aux data.
		// Closures are not useful to export anyway — plugins can't call them
		// by name. See mazdl investigation log for 2026-04-18.
		if strings.Contains(name, ".func") {
			continue
		}
		// Skip ABI0 wrapper entry points for text symbols. The loader carries
		// both an ABIInternal copy (version=1) AND an ABI0 adapter (version=0)
		// of every exported function under the *same* SymName; the static
		// symtab disambiguates by appending ".abi0" to the wrapper's name, but
		// dynsym emission uses SymExtname verbatim and would publish two
		// entries with identical names. When mazdl.Open populates its
		// globalSyms map keyed by name, the second write wins — which happens
		// to be the ABI0 wrapper. A plugin's ABIInternal call (arg in R0) then
		// targets the wrapper, which loads its arg from the stack at [sp+40]
		// and ignores R0 entirely, producing garbage (e.g. sync.init.1's
		// unsafe.Sizeof(notifyList) becomes a stack pointer and the host's
		// "bad notifyList size" check fires). Plugins built by mazgo are
		// internal-linked ABIInternal top-to-bottom, so they never need the
		// ABI0 adapter: export only the version=ABIInternal copy.
		if st.IsText() && ldr.SymVersion(i) == sym.SymVerABI0 {
			if alt := ldr.Lookup(name, sym.SymVerABIInternal); alt != 0 && ldr.SymType(alt).IsText() {
				continue
			}
		}
		if ldr.SymExtname(i) == "" {
			ldr.SetSymExtname(i, ldr.SymName(i))
		}
		ldr.SetAttrCgoExportDynamic(i, true)
		ctxt.dynexp = append(ctxt.dynexp, i)
		exported++
	}
	return exported
}
