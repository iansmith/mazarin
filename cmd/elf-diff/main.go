// elf-diff: structural comparison of two ELF files.
//
// Usage: go tool elf-diff <reference.elf> <candidate.elf>
//
// Built for mazlink bring-up: it compares a plugin produced by stock Go
// (cgo + external link — the known-good plugin shape) against a plugin
// produced by mazlink, and prints a punch list of what the mazlink output
// is still missing. Non-mazlink uses are fine too; the tool is generic.
//
// What it compares:
//   - ELF header (class, type, machine, entry)
//   - Program header summary (PT_LOAD counts, PT_DYNAMIC presence, relro, etc.)
//   - Section table (by name: presence, size, type, flags)
//   - .dynamic entries (DT_* tags and their targets where meaningful)
//   - .dynsym contents (symbol name, bind, type, section)
//   - Relocation sections (.rela.* — count, types histogram)
//   - .init_array contents (symbol names the dynamic loader will call)
//   - Exported "plugin-relevant" symbols (addmoduledata, firstmoduledata,
//     go:plugin.*, etc.)
//
// Output: a short human-readable report. Exit code 0 always (the tool is a
// diagnostic, not a pass/fail gate — callers parse the report if they want
// a strict check).
package main

import (
	"debug/elf"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <reference.elf> <candidate.elf>\n", os.Args[0])
		os.Exit(2)
	}
	refPath, candPath := os.Args[1], os.Args[2]

	ref, err := load(refPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load %s: %v\n", refPath, err)
		os.Exit(1)
	}
	defer ref.f.Close()
	cand, err := load(candPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load %s: %v\n", candPath, err)
		os.Exit(1)
	}
	defer cand.f.Close()

	fmt.Printf("=== elf-diff\n")
	fmt.Printf("  reference: %s\n", refPath)
	fmt.Printf("  candidate: %s\n", candPath)
	fmt.Println()

	diffHeader(ref, cand)
	diffProgHeaders(ref, cand)
	diffSections(ref, cand)
	diffDynamic(ref, cand)
	diffDynSyms(ref, cand)
	diffRelocs(ref, cand)
	diffInitArray(ref, cand)
	diffPluginSymbols(ref, cand)
}

type elfInfo struct {
	path    string
	f       *elf.File
	raw     *os.File
	secByNm map[string]*elf.Section
	syms    []elf.Symbol // from .dynsym (DynamicSymbols)
	imports []elf.ImportedSymbol
	dynTags []elf.DynTag
	dynVals map[elf.DynTag][]uint64
}

func load(path string) (*elfInfo, error) {
	raw, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	f, err := elf.NewFile(raw)
	if err != nil {
		raw.Close()
		return nil, err
	}
	info := &elfInfo{
		path:    path,
		f:       f,
		raw:     raw,
		secByNm: make(map[string]*elf.Section),
		dynVals: make(map[elf.DynTag][]uint64),
	}
	for _, s := range f.Sections {
		info.secByNm[s.Name] = s
	}
	if syms, err := f.DynamicSymbols(); err == nil {
		info.syms = syms
	}
	if imp, err := f.ImportedSymbols(); err == nil {
		info.imports = imp
	}
	// DynValue panics if .dynamic is absent; guard it.
	if info.secByNm[".dynamic"] != nil {
		for _, tag := range knownDynTags {
			if vals, err := f.DynValue(tag); err == nil && len(vals) > 0 {
				info.dynTags = append(info.dynTags, tag)
				info.dynVals[tag] = vals
			}
		}
	}
	return info, nil
}

// knownDynTags is the set of DT_* tags we probe for. DynValue requires us
// to ask for each tag; there's no "enumerate every tag" API.
var knownDynTags = []elf.DynTag{
	elf.DT_NEEDED, elf.DT_PLTRELSZ, elf.DT_PLTGOT, elf.DT_HASH, elf.DT_STRTAB,
	elf.DT_SYMTAB, elf.DT_RELA, elf.DT_RELASZ, elf.DT_RELAENT, elf.DT_STRSZ,
	elf.DT_SYMENT, elf.DT_INIT, elf.DT_FINI, elf.DT_SONAME, elf.DT_RPATH,
	elf.DT_SYMBOLIC, elf.DT_REL, elf.DT_RELSZ, elf.DT_RELENT, elf.DT_PLTREL,
	elf.DT_DEBUG, elf.DT_TEXTREL, elf.DT_JMPREL, elf.DT_BIND_NOW,
	elf.DT_INIT_ARRAY, elf.DT_FINI_ARRAY, elf.DT_INIT_ARRAYSZ,
	elf.DT_FINI_ARRAYSZ, elf.DT_RUNPATH, elf.DT_FLAGS, elf.DT_PREINIT_ARRAY,
	elf.DT_PREINIT_ARRAYSZ, elf.DT_GNU_HASH, elf.DT_RELACOUNT, elf.DT_RELCOUNT,
	elf.DT_FLAGS_1, elf.DT_VERNEED, elf.DT_VERNEEDNUM, elf.DT_VERSYM,
}

// ---------- diff passes ----------

func diffHeader(ref, cand *elfInfo) {
	fmt.Println("--- ELF header")
	row := func(label, r, c string) {
		mark := "="
		if r != c {
			mark = "!"
		}
		fmt.Printf("  %s %-12s ref=%-20s cand=%s\n", mark, label, r, c)
	}
	row("class", ref.f.Class.String(), cand.f.Class.String())
	row("type", ref.f.Type.String(), cand.f.Type.String())
	row("machine", ref.f.Machine.String(), cand.f.Machine.String())
	row("entry", fmt.Sprintf("0x%x", ref.f.Entry), fmt.Sprintf("0x%x", cand.f.Entry))
	fmt.Println()
}

func diffProgHeaders(ref, cand *elfInfo) {
	fmt.Println("--- Program headers (summary)")
	summarize := func(info *elfInfo) map[string]int {
		m := map[string]int{}
		for _, p := range info.f.Progs {
			m[p.Type.String()]++
		}
		return m
	}
	rs, cs := summarize(ref), summarize(cand)
	keys := unionKeys(rs, cs)
	for _, k := range keys {
		mark := "="
		if rs[k] != cs[k] {
			mark = "!"
		}
		fmt.Printf("  %s %-22s ref=%d cand=%d\n", mark, k, rs[k], cs[k])
	}
	fmt.Println()
}

func diffSections(ref, cand *elfInfo) {
	fmt.Println("--- Sections (by name)")
	names := map[string]bool{}
	for n := range ref.secByNm {
		names[n] = true
	}
	for n := range cand.secByNm {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		r := ref.secByNm[n]
		c := cand.secByNm[n]
		switch {
		case r != nil && c == nil:
			fmt.Printf("  - MISSING in cand: %s (%s, %d bytes)\n", n, r.Type, r.Size)
		case r == nil && c != nil:
			fmt.Printf("  + EXTRA in cand:   %s (%s, %d bytes)\n", n, c.Type, c.Size)
		default:
			if r.Type != c.Type || r.Size != c.Size {
				fmt.Printf("  ! DIFFERS %s: ref=%s/%d cand=%s/%d\n", n, r.Type, r.Size, c.Type, c.Size)
			}
		}
	}
	fmt.Println()
}

func diffDynamic(ref, cand *elfInfo) {
	fmt.Println("--- .dynamic entries")
	all := map[elf.DynTag]bool{}
	for _, t := range ref.dynTags {
		all[t] = true
	}
	for _, t := range cand.dynTags {
		all[t] = true
	}
	tags := make([]elf.DynTag, 0, len(all))
	for t := range all {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	for _, t := range tags {
		r := ref.dynVals[t]
		c := cand.dynVals[t]
		if len(r) == 0 && len(c) == 0 {
			continue
		}
		mark := "="
		if !sameUints(r, c) {
			mark = "!"
		}
		fmt.Printf("  %s %-20s ref=%s cand=%s\n", mark, t, fmtUints(r), fmtUints(c))
	}
	fmt.Println()
}

func diffDynSyms(ref, cand *elfInfo) {
	fmt.Println("--- .dynsym (dynamic symbol table)")
	refMap := map[string]elf.Symbol{}
	for _, s := range ref.syms {
		refMap[s.Name] = s
	}
	candMap := map[string]elf.Symbol{}
	for _, s := range cand.syms {
		candMap[s.Name] = s
	}
	fmt.Printf("  ref count: %d   cand count: %d\n", len(ref.syms), len(cand.syms))

	// Missing in candidate — the important direction when cand=mazlink.
	var missing []string
	for n := range refMap {
		if _, ok := candMap[n]; !ok {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		fmt.Printf("  %d symbol(s) in ref but not cand:\n", len(missing))
		show := missing
		if len(show) > 30 {
			show = show[:30]
		}
		for _, n := range show {
			s := refMap[n]
			fmt.Printf("    - %s (bind=%s type=%s)\n", n, elf.ST_BIND(s.Info), elf.ST_TYPE(s.Info))
		}
		if len(missing) > len(show) {
			fmt.Printf("    ... (+%d more)\n", len(missing)-len(show))
		}
	}

	var extra []string
	for n := range candMap {
		if _, ok := refMap[n]; !ok {
			extra = append(extra, n)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		fmt.Printf("  %d symbol(s) in cand but not ref:\n", len(extra))
		show := extra
		if len(show) > 10 {
			show = show[:10]
		}
		for _, n := range show {
			fmt.Printf("    + %s\n", n)
		}
		if len(extra) > len(show) {
			fmt.Printf("    ... (+%d more)\n", len(extra)-len(show))
		}
	}
	fmt.Println()
}

func diffRelocs(ref, cand *elfInfo) {
	fmt.Println("--- Relocation sections")
	summarize := func(info *elfInfo) map[string]map[string]int {
		out := map[string]map[string]int{}
		for name, sec := range info.secByNm {
			if !strings.HasPrefix(name, ".rela") && !strings.HasPrefix(name, ".rel.") {
				continue
			}
			if sec.Type != elf.SHT_RELA && sec.Type != elf.SHT_REL {
				continue
			}
			data, err := sec.Data()
			if err != nil {
				continue
			}
			histogram := map[string]int{}
			entSize := int(sec.Entsize)
			if entSize == 0 {
				continue
			}
			for off := 0; off+entSize <= len(data); off += entSize {
				var rawType uint32
				if info.f.Class == elf.ELFCLASS64 {
					// r_info is at offset 8 (size 8), type is low 32 bits.
					infoField := info.f.ByteOrder.Uint64(data[off+8:])
					rawType = uint32(infoField & 0xffffffff)
				} else {
					infoField := info.f.ByteOrder.Uint32(data[off+4:])
					rawType = infoField & 0xff
				}
				histogram[relocTypeName(info.f.Machine, rawType)]++
			}
			out[name] = histogram
		}
		return out
	}
	rs, cs := summarize(ref), summarize(cand)
	all := map[string]bool{}
	for k := range rs {
		all[k] = true
	}
	for k := range cs {
		all[k] = true
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		rh := rs[name]
		ch := cs[name]
		if rh == nil {
			fmt.Printf("  + cand has %s (not in ref)\n", name)
		}
		if ch == nil {
			fmt.Printf("  - ref has %s but cand does not\n", name)
			for t, n := range rh {
				fmt.Printf("      ref[%s] = %d\n", t, n)
			}
			continue
		}
		if rh == nil {
			for t, n := range ch {
				fmt.Printf("      cand[%s] = %d\n", t, n)
			}
			continue
		}
		types := map[string]bool{}
		for t := range rh {
			types[t] = true
		}
		for t := range ch {
			types[t] = true
		}
		tList := make([]string, 0, len(types))
		for t := range types {
			tList = append(tList, t)
		}
		sort.Strings(tList)
		fmt.Printf("  %s:\n", name)
		for _, t := range tList {
			mark := "="
			if rh[t] != ch[t] {
				mark = "!"
			}
			fmt.Printf("      %s %-28s ref=%d cand=%d\n", mark, t, rh[t], ch[t])
		}
	}
	fmt.Println()
}

func diffInitArray(ref, cand *elfInfo) {
	fmt.Println("--- .init_array")
	dump := func(info *elfInfo) []string {
		sec := info.secByNm[".init_array"]
		if sec == nil {
			return nil
		}
		data, err := sec.Data()
		if err != nil {
			return []string{fmt.Sprintf("(read error: %v)", err)}
		}
		ptrSize := 8
		if info.f.Class == elf.ELFCLASS32 {
			ptrSize = 4
		}
		names := []string{}
		for off := 0; off+ptrSize <= len(data); off += ptrSize {
			var addr uint64
			if ptrSize == 8 {
				addr = info.f.ByteOrder.Uint64(data[off:])
			} else {
				addr = uint64(info.f.ByteOrder.Uint32(data[off:]))
			}
			names = append(names, symbolAt(info, addr))
		}
		return names
	}
	rn := dump(ref)
	cn := dump(cand)
	fmt.Printf("  ref:  %d entry/entries\n", len(rn))
	for _, n := range rn {
		fmt.Printf("    - %s\n", n)
	}
	fmt.Printf("  cand: %d entry/entries\n", len(cn))
	for _, n := range cn {
		fmt.Printf("    - %s\n", n)
	}
	fmt.Println()
}

func diffPluginSymbols(ref, cand *elfInfo) {
	// Known markers of a working Go plugin. Presence or absence here tells
	// us whether the moduledata-registration path is wired up.
	markers := []string{
		"go:link.addmoduledata",
		"runtime.addmoduledata",
		"runtime.firstmoduledata",
		"runtime.pluginftabverify",
		"runtime.plugin_lastmoduleinit",
		"_cgo_topofstack",
	}
	fmt.Println("--- Plugin-relevant symbols")
	check := func(info *elfInfo, name string) string {
		for _, s := range info.syms {
			if s.Name == name {
				return fmt.Sprintf("bind=%s type=%s addr=0x%x", elf.ST_BIND(s.Info), elf.ST_TYPE(s.Info), s.Value)
			}
		}
		return "—"
	}
	for _, name := range markers {
		r := check(ref, name)
		c := check(cand, name)
		mark := "="
		if (r == "—") != (c == "—") {
			mark = "!"
		}
		fmt.Printf("  %s %-40s ref=[%s] cand=[%s]\n", mark, name, r, c)
	}
	fmt.Println()
}

// ---------- helpers ----------

func unionKeys(a, b map[string]int) []string {
	s := map[string]bool{}
	for k := range a {
		s[k] = true
	}
	for k := range b {
		s[k] = true
	}
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameUints(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fmtUints(v []uint64) string {
	if len(v) == 0 {
		return "—"
	}
	parts := make([]string, len(v))
	for i, u := range v {
		parts[i] = fmt.Sprintf("0x%x", u)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func symbolAt(info *elfInfo, addr uint64) string {
	if addr == 0 {
		return "(null)"
	}
	for _, s := range info.syms {
		if s.Value == addr && s.Name != "" {
			return fmt.Sprintf("%s (0x%x)", s.Name, addr)
		}
	}
	// Fall back to regular symbol table.
	if syms, err := info.f.Symbols(); err == nil {
		for _, s := range syms {
			if s.Value == addr && s.Name != "" {
				return fmt.Sprintf("%s (0x%x)", s.Name, addr)
			}
		}
	}
	return fmt.Sprintf("0x%x (no symbol)", addr)
}

func relocTypeName(m elf.Machine, t uint32) string {
	switch m {
	case elf.EM_AARCH64:
		return elf.R_AARCH64(t).String()
	case elf.EM_X86_64:
		return elf.R_X86_64(t).String()
	case elf.EM_386:
		return elf.R_386(t).String()
	}
	return fmt.Sprintf("R_%s_%d", m, t)
}
