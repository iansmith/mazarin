# MAZARIN-DLOPEN — implementation plan

Status: 2026-04-18. Companion to `docs/mazlink-gaps-and-dlopen-plan.md`.
That doc frames the problem and the end-state; this doc is the concrete
plan — policy, ABI, phased work, exit criteria.

Scope: Mazarin-native dynamic linking for Go plugins (`plugin.maz`), built
with our patched Go 1.26.2 internal linker. No cgo, no external linker,
no glibc. Produces plugins that ship **zero runtime code**, resolve
`runtime.*` (and eventually more) against the host at load time, and are
managed through a Mazarin-native `dlopen`/`dlsym` API.

Initial targets: `linux/amd64`, `linux/arm64`. `linux/riscv64` is deferred
per `design/GO-PLUGIN-SPIKE.md` (riscv64 needs PIE codegen built first).

---

## 1. Architecture overview

Four pieces, with clear ownership:

| Piece | Lives in | Responsibility |
|---|---|---|
| **mazlink** (linker patches) | `mazlink-patches/cmd/link/` | Emit plugin-shape ELF: UNDEF dynsym for host imports, PLT/JUMP_SLOT for function imports, GLOB_DAT for data imports, strip unreferenced host-package code, NOP host-package `init.N` entries. Also: emit host's export dynsym when linking the host binary. |
| **mazdl** (userspace library) | `mazarin/mazdl/` | Real dlopen: mmap segments via a thin kernel primitive, apply `R_*_RELATIVE`, resolve UNDEF symbols against the global module table, patch GOT/PLT, run `DT_INIT_ARRAY`, return a handle. `dlsym` walks the handle's export table. |
| **Kernel** | `kmazarin/ksyscall/` | A single new primitive: `SysMapELFSegment(fd, offset, len, vaddr, perms)` — mmap + W^X enforcement. Nothing about ELF parsing, nothing about relocations, nothing about symbol names. Legacy `SysLoadMaz` stays during migration, retires at end of Phase 5. |
| **maz-reloc** | `cmd/maz-reloc/` | Retires on arm64/amd64 once Phase 3 lands (the thin-stub trampoline work exists only because plugins today ship their own runtime). Stays alive for riscv64 until the spike doc's Phase 2 completes. |

Rule of thumb: if the work requires understanding ELF structure, it
lives in `mazdl`, not the kernel. The kernel only touches page tables.

---

## 2. Policy list — what lives in the host

The policy list is a plain-text file in the mazlink repo:
`mazlink-patches/policy/dlopen-host-packages.txt`. The linker reads it
at build time (`-dlopen-host-packages=<file>`).

Format: one Go import path or prefix per line. A trailing `/...` means
"this package and all subpackages." Comments start with `#`.

### Phase-2 starting policy (narrow)

Verified against Go 1.26.2 layout (path changes from older Go:
`runtime/internal/*` moved under `internal/runtime/*`, and `internal/sys`
is gone — its contents live under `internal/runtime/sys` now):

```
runtime
internal/runtime/...
internal/abi
internal/cpu
internal/bytealg
internal/goarch
internal/goos
internal/goexperiment
```

`internal/runtime/...` covers: `atomic`, `gc`, `maps`, `math`, `sys`,
`syscall`, `exithook`, `cgroup`, `pprof`, `startlinetest`. All are
runtime machinery; sharing across host/plugin is required for
correctness (atomics especially — CAS primitives must be identical on
shared memory).

**Why just these initially:** this is the minimum set that kills every
runtime-singleton goroutine (forcegchelper, sysmon, bgsweep, bgscavenge,
runfinq, gcBgMarkWorker, templateThread). Everything else continues to
ship statically in the plugin. The current mazlink GLOB_DAT path for
data references already assumes `runtime.*` is host-resident; Phase 2
extends that to function references.

### Phase-6 expansion (deliberate, incremental)

Add only when a specific bug or requirement forces it. Each addition
gets a short note in this file explaining why. Candidates and their
triggers:

| Package | Trigger for moving to host |
|---|---|
| `sync`, `sync/atomic` | Plugin-vs-host races on package-level mutexes; also: per-plugin copy of `sync.Mutex` semaphore wait lists would diverge from runtime's semroot |
| `reflect` | Cross-module type identity requires single `reflect.Type` table |
| `os` | `os.Stdin`/`Stdout`/`Stderr` singletons; `os.Args` |
| `time` | `time.runtimeTimer` goroutine is a singleton |
| `log` | Package-level default logger + mutex |
| `os/signal` | Dispatch goroutine is a singleton |

### Explicit non-host packages (always in plugin)

`fmt`, `strings`, `strconv`, `errors`, `encoding/...`, `bytes`, `unicode`,
`sort`, `slices`, `maps`, `math`, `io`, `bufio`, `context` — pure-data
or per-instance packages. No singletons, no shared state, fine to
duplicate per plugin.

### Ambiguous — requires explicit decision before including

- `internal/poll` — has a pollDesc pool per netFD; probably per-plugin is fine, but confirm.
- `syscall` — thin wrappers around Linux syscalls; per-plugin is fine, but `syscall.Environ` singleton means `os.Environ` changes don't propagate.
- `unsafe` — compiler intrinsics, not a real package.

---

## 3. ABI contract: what a `plugin.maz` looks like

A Mazarin dlopen-compatible plugin is a **dynamic ELF** (`ET_DYN`) with
the following layout. This is standard System V ABI; the only novelty
is that the "dynamic linker" is our userspace `mazdl`, not glibc's
`ld.so`.

### Required sections

| Section | Contents |
|---|---|
| `.dynsym` | One UNDEF entry per host-imported symbol; one DEFINED entry per exported symbol (`MazarinMain`, `PluginAPI_Hello`, etc.) |
| `.dynstr` | Strings backing `.dynsym` |
| `.rela.dyn` | `R_*_RELATIVE` for internal pointers (same as PIE today); `R_*_GLOB_DAT` for each UNDEF data symbol (points at the GOT slot to fill) |
| `.rela.plt` | `R_*_JUMP_SLOT` for each UNDEF function symbol (points at the `.got.plt` slot) |
| `.plt` | One stub per imported function: loads target from `.got.plt`, jumps |
| `.got` | One slot per imported data symbol; relocated by `.rela.dyn` at load time |
| `.got.plt` | One slot per imported function symbol; relocated by `.rela.plt` at load time. Eager binding (no lazy resolver) — `mazdl` fills every slot before returning from `Open` |
| `.dynamic` | Tags: `DT_NEEDED`, `DT_STRTAB`, `DT_SYMTAB`, `DT_RELA`, `DT_RELASZ`, `DT_JMPREL`, `DT_PLTREL=DT_RELA`, `DT_PLTRELSZ`, `DT_PLTGOT`, `DT_INIT_ARRAY`, `DT_INIT_ARRAYSZ`, `DT_SONAME` |
| `.init_array` | Sequence of function pointers: `addmoduledata` wrapper first, then user inits |

### `DT_NEEDED` entries

Each plugin declares which "host" it expects to be loaded into. The
MVP uses a single string: `"mazarin-host"`. `mazdl.Open` verifies the
current process exports a module named `mazarin-host` before binding.

### Eager binding, not lazy

Stock glibc populates `.got.plt` with a trampoline to the lazy
resolver; slots get filled on first call. **We don't do this.**
Reasons:

- No runtime resolver means no lazy-bind trampoline in the plugin.
- Simpler error story: `Open` fails immediately if any symbol is
  unresolved, rather than panicking later in a random call site.
- Reproducibility: no dependency on call order during debugging.

`mazdl.Open` resolves every `R_*_JUMP_SLOT` up front. `.plt` stubs
exist purely to keep the `call`/`bl` instruction small; they load
from `.got.plt` and jump.

### Relocation types we emit/consume

| Arch | Data | Function |
|---|---|---|
| amd64 | `R_X86_64_RELATIVE`, `R_X86_64_GLOB_DAT` | `R_X86_64_JUMP_SLOT`, `R_X86_64_PLT32` (call-site against `.plt`) |
| arm64 | `R_AARCH64_RELATIVE`, `R_AARCH64_GLOB_DAT` | `R_AARCH64_JUMP_SLOT`, `R_AARCH64_CALL26`/`R_AARCH64_JUMP26` (call-site against `.plt`) |

`R_*_COPY` is **not emitted** and **not supported**. We always
indirect through GOT; there is a single authoritative copy of every
host datum.

### Host-policy funcvals (option A — landed 2026-04-18)

Go emits an 8-byte `.data.rel.ro` object for every function that is
taken as a value (`funcval`, name ends in `·f`, U+00B7 MIDDLE DOT +
`f`). Its single field `.fn` holds the function's PC. Map types,
interface tables, `reflect.Value.Call`, and compiler-generated
closures read `.fn` and branch through it.

**Problem:** When mazlink strips a host-policy package's code from
the plugin, the `.text` symbol disappears but the funcval object in
`.data.rel.ro` stays (other plugin code still references
`&runtime.strhash·f` etc.). The original emission wrote the
funcval's `.fn` as `R_*_RELATIVE` with addend = the *would-be*
plugin-relative address of the stripped function, which lands in
the zero-padding gap between the last real plugin `.text` function
and `runtime.etext`. Any indirect call through the funcval (e.g.
`runtime.mapassign_faststr` reading `maptype.Hasher`) branched into
padding → `udf #0` / SIGILL.

**Fix (landed):** In `adddynrel`'s `R_ADDR` case, before the generic
`R_*_RELATIVE` fallback, check whether the target is `SDYNIMPORT`
*and* its `DynimpLib` is `"mazarin-host"`. When both hold, the
target is a host-policy symbol that `rewriteHostSymsAsDynimport`
flipped, so emit the reloc as `R_AARCH64_GLOB_DAT` /
`R_X86_64_GLOB_DAT` against the target's dynsym entry (adding it
via `ld.Adddynsym` first). The dynamic loader (`mazdl.Open`) writes
the host's real address at `r_offset` during its reloc pass. The
`DynimpLib == "mazarin-host"` gate is load-bearing: it restricts the
rewrite to symbols this policy pass explicitly stripped, so we don't
accidentally promote unrelated `SDYNIMPORT` entries (static externs,
anonymous type descriptors) that have no host counterpart. Home:
- `mazlink-patches/cmd/link/internal/arm64/asm.go`
- `mazlink-patches/cmd/link/internal/amd64/asm.go`

**Name-mangling parity for host exports.** The plugin is built as
`BuildModePlugin`, so `ld.mangleTypeSym` hashes every long `type:.*`
symbol to a 6-byte base64 tag as its dynsym `extname` (e.g.
`type:.eq.runtime._func` → `type:.C9kB2TSL`). The host is built as
`BuildModeExe` and stock `mangleTypeSym` bails out for exe mode,
so the host would export the unhashed name while the plugin looks
up the hash → "unresolved symbol" at load time. Mazlink patches
`mangleTypeSym` to also run when `-dlopen-host-exports` is set, so
host and plugin dynsym names match. This is what turned up the
`type:.C9kB2TSL` failure during Option A bring-up: the reloc was
correct, the lookup name was not.

See `memory/mazlink_funcval_dead_reloc_bug.md` for the full
post-mortem and disassembly of the original SIGILL case.

### Symbol-versioning

Not emitted in MVP. Host and plugin are built in lockstep from the
same Go toolchain; version skew is a non-concern within a Mazarin
release. Add `DT_VERSYM` / `DT_VERNEED` later if we ever need
cross-release compatibility.

---

## 4. Mazlink changes — file-by-file

Baseline: `mazlink-patches/cmd/link/internal/` currently patches
`amd64/asm.go`, `arm64/asm.go`, `ld/config.go`, `ld/data.go`,
`ld/elf.go`, `ld/go.go`, `ld/lib.go`. All of these get further edits;
no new files except `ld/mazdl.go` (new central dispatcher for the
policy and dynsym emission) and the policy text file.

### New files

**`mazlink-patches/policy/dlopen-host-packages.txt`**
The policy list (see §2). Checked into repo, read by linker via
`-dlopen-host-packages=<path>`.

**`mazlink-patches/cmd/link/internal/ld/mazdl.go`** (new)
Central dispatcher. Owns:

- `loadHostPolicy(path string) *HostPolicy` — parses the policy file, returns a prefix-matching matcher.
- `isHostSymbol(sym *sym.Symbol, p *HostPolicy) bool` — given a symbol, answers "this belongs in the host."
- `rewriteHostSymsAsDynimport(ctxt *Link, p *HostPolicy)` — walks all symbols, marks host-policy matches as `sym.SDYNIMPORT`, nulls their content so deadcode can drop the bodies.
- `emitHostExportsDynsym(ctxt *Link, exportsFile string)` — when linking a *host* binary, reads the exports file and marks listed symbols `GLOBAL | DEFINED` in output `.dynsym`.

One file, ~400 LOC, keeps the policy code out of the existing patches.

### Modified files

#### `mazlink-patches/cmd/link/internal/ld/config.go`

Add flags:

```go
flagDlopenHostPackages = flag.String("dlopen-host-packages", "",
    "path to policy file listing packages resolved against host (plugin mode)")
flagDlopenHostExports  = flag.String("dlopen-host-exports", "",
    "path to exports file; this binary exports listed symbols as dynsym (host mode)")
flagDlopenNopInitN     = flag.Bool("dlopen-nop-init", false,
    "NOP all host-policy-matched init.N functions in the plugin")
```

`dlopen-host-packages` and `dlopen-host-exports` are mutually
exclusive — the first makes this build a plugin, the second makes it
a host. Neither set: legacy mazlink behavior (current plugin-shape
with full runtime + GLOB_DAT only).

#### `mazlink-patches/cmd/link/internal/ld/lib.go`

Early in `(*Link).loadlib`, after packages are loaded:

1. If `flagDlopenHostPackages != ""`, call `mazdl.loadHostPolicy` and
   `mazdl.rewriteHostSymsAsDynimport`. This must run *before*
   deadcode so that host-package bodies become unreferenced and get
   swept.
2. If `flagDlopenHostExports != ""`, call `mazdl.emitHostExportsDynsym`
   during the dynsym-building phase (later in `asmb2`).

Keep the existing "mazlink: plugin has no entry symbol" comment logic
intact — that's orthogonal.

#### `mazlink-patches/cmd/link/internal/ld/deadcode.go`

One addition: after the standard reachability walk, for any symbol
marked `SDYNIMPORT`, ensure its *body* (content bytes) is
nil-ed so file size drops even if some path still has a lingering
reference we haven't found. Safety net.

#### `mazlink-patches/cmd/link/internal/ld/elf.go`

- Add `.plt`, `.got.plt` section emission in plugin mode (gated on `flagDlopenHostPackages`).
- Emit `.rela.plt` for every `R_*_JUMP_SLOT` relocation.
- Populate `.dynamic` with `DT_JMPREL`, `DT_PLTRELSZ`, `DT_PLTREL=DT_RELA`, `DT_PLTGOT`.
- Emit `DT_NEEDED` entry for the single string `"mazarin-host"`.

Most of this exists in Go's linker already for c-shared / c-archive
modes — we're enabling a subset of that code path for our plugin mode.

#### `mazlink-patches/cmd/link/internal/ld/data.go`

- Size `.plt` as `pltHeader + 16 * numJumpSlots` (amd64) / `32 + 16 * numJumpSlots` (arm64).
- Size `.got.plt` as `3 * 8 + 8 * numJumpSlots` (first three slots reserved by System V ABI; we leave them zero since we eager-bind).

#### `mazlink-patches/cmd/link/internal/amd64/asm.go`

- For any call-site whose target is `SDYNIMPORT`, emit `R_X86_64_PLT32` against the `.plt` stub for that symbol.
- Keep existing `R_X86_64_GOTPCREL` path for data refs (already emits `GLOB_DAT`).
- PLT stub sequence (16 bytes each): `jmp *got_plt_offset(%rip); push $reloc_index; jmp plt0`.

#### `mazlink-patches/cmd/link/internal/arm64/asm.go`

- For any `BL`/`B` targeting `SDYNIMPORT`, emit `R_AARCH64_CALL26`/`JUMP26` against the `.plt` stub.
- Existing `ADR_GOT_PAGE`/`LD64_GOT_LO12_NC` path stays for data.
- PLT stub: `adrp x16, got_plt; ldr x17, [x16, #off]; br x17`.

#### `mazlink-patches/cmd/link/internal/ld/go.go`

- Ensure host-package `init.N` functions get dropped. When
  `flagDlopenNopInitN` is set, any symbol matching the regex
  `^<hostpkg>\.init\.\d+$` (for `hostpkg` in the policy list) is
  replaced with a function body that is a single RET. Preserves the
  symbol (so `runtime.inittasks` table entries still resolve) but
  makes it a no-op.

### Concrete LOC estimate (patches only, not stdlib source)

| File | Existing | Added |
|---|---|---|
| `ld/mazdl.go` (new) | 0 | ~400 |
| `ld/lib.go` | ~50 | ~80 |
| `ld/elf.go` | ~60 | ~200 |
| `ld/data.go` | ~30 | ~60 |
| `ld/deadcode.go` | 0 | ~30 |
| `ld/go.go` | ~40 | ~50 |
| `ld/config.go` | ~20 | ~30 |
| `amd64/asm.go` | ~180 | ~80 |
| `arm64/asm.go` | ~200 | ~100 |
| Policy file | 0 | ~30 |
| **Total** | ~580 | ~1060 |

Roughly doubles the mazlink patch footprint temporarily; Phase 4's
retirement of thin-stub patches reclaims ~200 LOC back.

---

## 5. Host-side runtime exports

The host binary (the single shepherd process — see
`memory/shepherd_plugin_model.md`) needs a `.dynsym` table that
*exports* every symbol the policy file names as host-resident.
`kmazarin` is **not** a host; it launches the shepherd and hands it a
starting plugin path, and the shepherd is where `mazdl.Open` runs.

### Exports file

`mazlink-patches/policy/dlopen-host-exports.txt` — auto-generated from
the host-packages file by a small tool at linker-build time. The tool
walks the host binary's symbol table, matches against the policy
prefixes, and writes out the list of `GLOBAL | DEFINED` exports.

Alternatively: mazlink can do this inline when
`-dlopen-host-exports` points at the host-packages file directly.
Start with that simpler path; extract the tool later if the list
needs hand-curation.

### Export surface concerns

- **Name stability across Go versions.** `runtime.mallocgc`'s ABI can
  shift between Go releases. Since host and plugin build from the
  same toolchain in the same Mazarin release, this is fine in
  practice. If we ever ship a plugin built against a different Go
  version, we'll need symbol-versioning (`DT_VERSYM`).
- **Hidden/local symbols.** Go marks many `runtime.*` helpers as
  `STB_LOCAL` for inlining. Exporting them requires promoting to
  `STB_GLOBAL`. mazlink already does this selectively for the current
  GLOB_DAT work; extend the same promotion to function symbols.
- **Go's `cutab`, `funcdata`, `pclntab`.** Not called across module
  boundaries — the plugin's pclntab handles its own frames.
  No special handling.

### Which symbols matter in practice

For the narrow Phase-2 policy list, the expected UNDEF-symbol count
in the plugin is **~250–400**, dominated by:

- `runtime.mallocgc`, `runtime.newobject`, `runtime.newarray`, `runtime.makeslice`
- `runtime.gopark`, `runtime.goready`, `runtime.chansend`, `runtime.chanrecv`
- `runtime.mapassign`, `runtime.mapaccess*`, `runtime.mapiterinit`, `runtime.mapdelete`
- `runtime.growslice`, `runtime.typedslicecopy`, `runtime.typedmemmove`
- `runtime.convI2I`, `runtime.assertI2T`, `runtime.panicdottypeI`
- `runtime.gcWriteBarrier`, `runtime.morestack`, `runtime.newstack`
- Data: `runtime.sched`, `runtime.allm`, `runtime.forcegc`, `runtime.memstats`, `runtime.writeBarrier`

The full list is computed; not curated. Anything the plugin
references that the policy marks as host-resident becomes UNDEF
automatically.

---

## 6. `mazdl` library design

Location: `mazarin/mazdl/`.

### Public API

```go
package mazdl

type Handle struct {
    // opaque — fields unexported
}

// Open loads plugin.maz into the current address space, resolves all
// UNDEF symbols against the global module table, runs init-array
// entries, and returns a handle.
func Open(filename string) (*Handle, *merror.Error)

// Sym looks up an exported symbol by name and returns its resolved
// absolute address.
func (h *Handle) Sym(name string) (uintptr, *merror.Error)

// Name returns the SONAME the plugin declared (or the filename if
// none).
func (h *Handle) Name() string

// Close is a stub in MVP (returns nil). Future: munmap + finalize.
func (h *Handle) Close() *merror.Error
```

### Internal structure

```
mazarin/mazdl/
├── open.go         // Open() orchestration
├── mmap.go         // SysMapELFSegment wrappers, permission flips
├── elfread.go      // parse ELF headers from memory-mapped file
│                    //   (mazarin-internal reader; stdlib debug/elf
│                    //    rejects some of our layouts, see below)
├── reloc_amd64.go  // apply R_X86_64_{RELATIVE,GLOB_DAT,JUMP_SLOT}
├── reloc_arm64.go  // apply R_AARCH64_{RELATIVE,GLOB_DAT,JUMP_SLOT}
├── resolve.go      // walk UNDEF dynsym, fill GOT/PLT slots
├── registry.go     // global module table, protected by sync.Mutex
├── initarray.go    // walk DT_INIT_ARRAY, call entries
├── handle.go       // Handle struct, Sym implementation
└── host_register.go // host-side self-registration
                      //   (host calls mazdl.RegisterHost at startup)
```

### Global module table

```go
// registry.go
var (
    modulesMu sync.Mutex
    modules   = map[string]*Handle{}  // keyed by SONAME

    // Exports across all loaded modules. When Open adds a module,
    // it appends this module's DEFINED dynsym entries here.
    globalSyms = map[string]symEntry{}
)

type symEntry struct {
    addr   uintptr
    module *Handle
    kind   symKind  // function or data
}
```

The host self-registers at process start:

```go
// Called from userspace main before any mazdl.Open.
func RegisterHost() {
    h := buildHostHandle()  // reads host's own .dynsym
    modulesMu.Lock()
    modules["mazarin-host"] = h
    for name, addr := range h.exports {
        globalSyms[name] = symEntry{addr: addr, module: h, kind: ...}
    }
    modulesMu.Unlock()
}
```

### `Open` orchestration

```go
func Open(filename string) (*Handle, *merror.Error) {
    // 1. Read ELF into memory (not mmapped — we parse headers first)
    //    Use mazarin/mazdl/elfread, not debug/elf (see below).
    elfData, err := readFileAll(filename)
    if err != nil { return nil, err }
    elf, err := parseELF(elfData)
    if err != nil { return nil, err }

    // 2. Reserve contiguous VA range and ask kernel to map each
    //    PT_LOAD at base+p_vaddr with the right perms. Kernel
    //    enforces W^X.
    base, err := reserveVA(elf.loadSize())
    if err != nil { return nil, err }
    for _, ph := range elf.loads() {
        err := sys.MapELFSegment(elfFd, ph.offset, ph.filesz, base+ph.vaddr, ph.perms)
        if err != nil { return nil, err }
        // bss zero-fill for memsz > filesz
    }

    // 3. Apply R_*_RELATIVE — self-contained, no symbol lookup.
    applyRelative(base, elf.relaDyn())

    // 4. Verify DT_NEEDED. For MVP, only "mazarin-host" is accepted.
    if err := verifyNeeded(elf); err != nil { return nil, err }

    // 5. Resolve UNDEF symbols. Walk .rela.dyn and .rela.plt,
    //    look each target symbol up in globalSyms, write into the
    //    GOT/PLT slot.
    h := &Handle{base: base, soname: elf.soname()}
    if err := resolveAndBind(h, elf); err != nil { return nil, err }

    // 6. Publish this module's exports to globalSyms and modules.
    h.addExports(elf.definedDynsym())
    modulesMu.Lock()
    modules[h.soname] = h
    for name, addr := range h.exports {
        if _, exists := globalSyms[name]; exists {
            // Last-loaded wins, or error? MVP: error loudly.
            modulesMu.Unlock()
            return nil, merror.New("duplicate symbol: " + name)
        }
        globalSyms[name] = symEntry{addr: addr, module: h, ...}
    }
    modulesMu.Unlock()

    // 7. Flip segment perms: writable regions that should become RX
    //    get re-mprotected now (via SysMapELFSegment with new perms
    //    or a SysMprotect SVC — whichever already exists).
    finalizeSegPerms(base, elf)

    // 8. Run DT_INIT_ARRAY. The first entry will be an
    //    addmoduledata wrapper, which registers the plugin's
    //    firstmoduledata with the host runtime. Subsequent entries
    //    are user init funcs (e.g. package init for fmt, user code).
    //    Any init from a host-policy-matched package was NOP'd by
    //    the linker (Phase 2's -dlopen-nop-init flag).
    runInitArray(base, elf.initArray())

    return h, nil
}
```

### Why `mazarin/mazdl/elfread` and not `debug/elf`

Our plugins currently carry one stdlib-hostile quirk: Go's `-T` flag
produces negative file offsets for phdrs, which `debug/elf` rejects.
`maz-reloc` already works around this with a custom reader. Phase 4
moves that reader into `mazarin/mazdl/elfread` and extends it to the
parsing surface `mazdl` needs. Longer-term, if we drop `-T` (plugin
mode doesn't need it), we can reconsider.

### `Sym` implementation

```go
func (h *Handle) Sym(name string) (uintptr, *merror.Error) {
    modulesMu.Lock()
    defer modulesMu.Unlock()
    e, ok := h.exports[name]
    if !ok {
        return 0, merror.New("symbol not found: " + name)
    }
    return e.addr, nil
}
```

No searching across modules — `h.Sym` only returns symbols defined
*by* `h`. Cross-module lookup would use a hypothetical
`mazdl.LookupGlobal(name)` that's out of scope for MVP.

### Concurrency model

- `modulesMu` protects all mutations of `modules` and `globalSyms`.
- `Open` is serialized — two concurrent loads of different modules
  block each other. For MVP this is fine; module loading is rare.
- Reading `Sym` from a loaded handle is lock-protected but otherwise
  unserialized.
- Plugin code executing concurrently with another plugin's `Open` is
  safe: resolved GOT slots don't change after a plugin is loaded,
  and cross-plugin references aren't allowed in MVP (see §2).

---

## 7. Kernel changes

### New syscall

```go
// kmazarin/ksyscall/mapelfseg.go
//
// SysMapELFSegment maps a region of a file into the calling shepherd's
// address space at the requested VA with the requested permissions.
// Enforces W^X: perms must be one of R, RX, RW (never RWX).
func SysMapELFSegment(fd int32, fileOff uint64, length uint64,
    vaddr uint64, perms uint32) *merror.Error
```

Implementation: existing page-table plumbing (same code path as
`SysLoadMaz`'s segment-mapping stage, factored out). No ELF parsing,
no relocation, no symbol lookup.

### Retained (during migration)

`SysLoadMaz` stays unchanged through Phase 5. `mazdl.Open` uses the
new primitive; existing callers use the legacy one. Phase 5 migrates
everything and deletes `SysLoadMaz`.

### Removed (end of Phase 5)

- `kmazarin/ksyscall/loadmaz.go` — SysLoadMaz, MazLoadResult, LoadMazWorkRequest.
- `kmazarin/ksyscall/runmaz.go` — handled by `mazhost` using `mazdl` instead.
- The kernel's symbol-name hunt for `MazarinMain`, `MazarinShepherd`,
  `PriestInitAddr`, `runtime.writeBarrier`, `runtime.firstmoduledata`.

### `runtime.firstmoduledata` registration

Currently the kernel finds `runtime.firstmoduledata` by name after
`SysLoadMaz` and hands it back in `MazLoadResult.ModuledataAddr`.
Post-dlopen, the plugin's `DT_INIT_ARRAY` first entry is an
`addmoduledata` wrapper compiled into the plugin by mazlink. That
wrapper calls `runtime.addmoduledata` (resolved via GOT to host)
with `&runtime.firstmoduledata` (a plugin-local symbol).

The wrapper is a small linker-synthesized stub:

```go
// Synthesized by mazlink in every plugin build.
func _mazdl_register_moduledata() {
    runtime.addmoduledata(&runtime.firstmoduledata)
}
```

`runtime.addmoduledata` is a standard Go runtime entrypoint;
exporting it from the host is a single-line addition to the policy
file's export side. No custom kernel glue.

---

## 8. Migration plan — existing loaders

Current `.maz` loaders that hunt for symbol names:

| Loader | Hunts for | Phase-5 replacement |
|---|---|---|
| `mazarin/mazhost/load.go:LoadMazBootstrap` | `MazarinMain`, `MazarinShepherd` | `mazdl.Open` + `h.Sym("MazarinMain")` |
| Disk priest (`flock/cmd/disk/main.go`) | `MazarinMain`, `MazarinPriest` | Same as above |
| `kmazarin/ksyscall/loadmaz.go` | `runtime.firstmoduledata`, `runtime.writeBarrier` | Gone — kernel no longer looks for symbols |

Migration template (per-loader):

```go
// Before:
res, err := sys.LoadMaz(filename)
if err != nil { return err }
mazMain := bootstrap(res.EntryPoint)

// After:
h, err := mazdl.Open(filename)
if err != nil { return err }
entryAddr, err := h.Sym("MazarinMain")
if err != nil { return err }
fv := &funcval{fn: entryAddr}
mazMain := *(*func())(unsafe.Pointer(&fv))
```

The `funcval` dance, the `preGrowStack` call, and the goroutine
launch all stay — those are goroutine-entry plumbing, unrelated to
loading.

---

## 9. Phased ordering with exit criteria

Each phase has a concrete exit criterion — a test or artifact that
proves the phase worked. Phases are sequential except where noted.

### Phase 0 — unblock amd64 smoke test (small, isolated) — SHIPPED 2026-04-18

**Goal:** Stop the plugin's `runtime.init.N` functions from
spawning duplicate runtime-singleton goroutines (forcegchelper,
sysmon, bgsweep, bgscavenge, runfinq, gcBgMarkWorker,
templateThread) that the host already owns.

**What actually shipped (simpler than original spec):**
`mazlinkNopHostInitTasks` in `mazlink-patches/cmd/link/internal/ld/go.go`,
called from the end of `addexport()`. When `BuildModePlugin + LinkInternal`,
it looks up `runtime..inittask`, calls `sb.MakeWritable()` (loaded object
data is mmap'd read-only and segfaults on direct mutation), and writes
`state=2` (done) at offset 0 of the struct. `runtime.doInit1` sees
`state==2` and returns without invoking any of the listed init functions.

No flag. Default-on for the narrow shape this applies to (mazlink plugin +
internal link). Stock Go plugin mode uses the external linker and a
different code path — unaffected.

Deliberately *not* the original spec's approach of NOPing individual
`runtime.init.N` function bodies by regex — the inittask state flip is a
4-byte write with no instruction rewriting, no stackmap concerns, and no
per-function analysis. Same effect (no init functions run), much smaller
surface area.

**Exit criterion — met:** `smoke/host` loads `smoke/plugin` end-to-end on
both arm64 and linux/amd64 emulation. Output:
```
mazlink smoke: hello from mazlink plugin
mazlink smoke: bump=1 bump=2
mazlink smoke: stress=stressed 8 times
```
Pre-fix amd64 threw `forcegc: phase error` during plugin init; post-fix
the test completes.

**Open follow-up (not blocking):** smoke test doesn't yet dump
`runtime.Stack` to assert singleton goroutine count. Add in Phase 4
alongside the full mazdl handoff, where it becomes the natural regression
guard.

**Scope:** ~50 LOC to mazlink (one helper in `go.go` + one call site).
No kernel changes, no mazdl, no flag, no policy file yet.

### Phase 1 — design sign-off (no code) — SHIPPED 2026-04-18

**Goal:** User reviews §2 policy list and §3 ABI contract.

**What shipped:** This doc (§2 policy + §3 ABI) confirmed by Ian.
Policy file landed at `mazlink-patches/policy/dlopen-host-packages.txt`
with the Phase-2 starting set. Decisions:

- `internal/runtime/...` kept wholesale (sharing required for atomic CAS
  correctness on shared memory).
- `DT_NEEDED = "mazarin-host"` as a single well-known label —
  sanity check only, not a real dependency mechanism.
- Single authoritative copy of host data (no `R_*_COPY`).
- No symbol versioning — explicitly to avoid version-skew hell.
- Ambiguous middle-ground packages (`sync`, `reflect`, `os`, `time`,
  etc.) deferred to Phase 6, with the acknowledgment that several
  will likely need to land there.

**Exit criterion — met.** Phase 2 unblocked.

### Phase 2 — linker emits UNDEF dynsym + PLT (runtime-only)

**Goal:** Plugin builds with **no runtime code** except host-
resolved stubs. Policy file contains only the Phase-2 starting set.

**Work:**
- New file `ld/mazdl.go` with policy loader.
- `ld/lib.go` hook: `rewriteHostSymsAsDynimport`.
- `ld/elf.go`: emit `.plt`, `.got.plt`, `.rela.plt`, `DT_JMPREL`.
- `ld/data.go`: size `.plt`/`.got.plt`.
- `amd64/asm.go`, `arm64/asm.go`: emit `PLT32`/`CALL26` for DYNIMPORT calls.
- `ld/go.go`: synthesize `_mazdl_register_moduledata` init-array entry.

**Exit criterion:**
1. `nm plugin.maz | grep ' U ' | wc -l` shows 200+ UNDEF symbols
   (all from `runtime.*`).
2. `nm plugin.maz | grep ' T runtime\.' | wc -l` is **zero**
   (no runtime code shipped).
3. Plugin binary size: < 1 MB (was ~6 MB).
4. `readelf -d plugin.maz` shows `DT_JMPREL`, `DT_PLTRELSZ`,
   `DT_PLTREL=DT_RELA`, `DT_PLTGOT`, `DT_NEEDED mazarin-host`.
5. Plugin does not run yet — expected. Loader side comes next.

**Scope:** ~700 LOC to mazlink.

### Phase 3 — host exports its runtime dynsym

**Goal:** any binary built with `-dlopen-host-exports=<policy>`
contains every policy-matched package's `T` symbols as
`GLOBAL | DEFINED` in its own `.dynsym`, so that Phase-2 plugins can
bind their UNDEF entries against it at load time.

**Note on scope — updated 2026-04-18:** the original draft had this
phase wire the flag into the kmazarin and shepherd link commands.
Post-update the plan is: there is exactly **one** shepherd program
(see `memory/shepherd_plugin_model.md`) and that shepherd does not
exist in the codebase yet — it's created in Phase 4 when it has a
job to do (calling `mazdl.Open`). `kmazarin` is not a host; the
shepherd is. Phase 3 therefore lands the linker work only and
proves it against `smoke/host-probe`, which is structurally what
the shepherd's host-export shape will be. Build-toolchain
integration (how we plumb `bin/mazgo`/`bin/mazlink` into the darwin
build of the shepherd) is deferred to Phase 4 and designed
alongside the shepherd itself.

**Work:**
- Extend `ld/mazdl.go` with `emitHostExportsDynsym`.
- `-dlopen-host-exports=<file>` flag; point it at the policy file.
- Filter closures (`.func*` suffixes) — they share pclntab aux
  syms with their outer function; marking them as independent
  dynexp roots crashes pclntab generation.
- Force `havedynamic = 1` so stock linksetup doesn't suppress
  `.dynsym` for an exe with no shared-lib imports.
- `smoke/host-probe/` — minimal Go program linked with the flag,
  run inside the smoke container, validates dynsym shape.

**Exit criterion (revised):**
1. `nm /tmp/host-probe | grep ' T runtime\.mallocgc'` resolves to
   a real address.
2. `readelf --dyn-syms /tmp/host-probe` shows `runtime.mallocgc`
   as `GLOBAL DEFAULT FUNC` (not `LOCAL HIDDEN`, not `UND`).
3. Exported dynsym counts are substantial: hundreds of
   `runtime.*`, `internal/runtime/...`, and `internal/abi.*`
   entries (actual run: 3292 / 418 / 423 on arm64).
4. `host-probe` runs and produces correct output (proves the
   modified linker didn't break anything else).

**Scope:** ~200 LOC to mazlink + smoke test edits (no Taskfile
rewiring of kmazarin/shepherd — that happens in Phase 4).

### Phase 4 — `mazdl` library + new kernel primitive

**Goal:** Userspace `mazdl.Open` loads a plugin built with Phase-2
linker against a Phase-3 host, and resolves everything.

**Work:**
- New syscall `SysMapELFSegment` in `kmazarin/ksyscall/`.
- `mazarin/mazdl/` package per §6 structure.
- `mazarin/mazdl/elfread/` ELF parser (copy from maz-reloc, extend).
- Host-side `mazdl.RegisterHost` called at userspace main.

**Exit criterion:**
1. `smoke/host-mazdl` on amd64 and arm64 calls
   `mazdl.Open("plugin.maz")`, receives handle, calls
   `h.Sym("Hello")`, invokes the returned function pointer, gets
   `"hello from mazlink plugin"`.
2. `runtime.Stack` on the host shows **exactly one**
   `forcegchelper`, `sysmon`, `bgsweep`, `bgscavenge`, `runfinq`.
   (`sysmon` runs on an M without a G so it doesn't appear in
   `runtime.Stack`; the check covers the other four and requires
   `count <= 1` each.)
3. Plugin allocations visible in host `runtime.memstats`
   (single heap). Smoke asserts `TotalAlloc` delta after
   `stress(1000)` exceeds a threshold.
4. 1000-iteration `Stress()` test runs clean with no panics or
   data races.

**Status (2026-04-18):** arm64 passes all four exits via
`$GO tool task mazlink-smoke` (see `smoke/host-mazdl/main.go` +
`smoke/run-smoke.sh`). Option A for host-policy funcvals (see §3)
landed on both arches and the loader-side `rewriteHostFuncvals`
workaround has been removed from `mazdl/open.go`.

**Open work to close Phase 4:**
- **amd64 parity.** Exits #1-#4 must pass on amd64 as well. Option A
  is already present in `mazlink-patches/cmd/link/internal/amd64/asm.go`
  mirroring the arm64 block, but runtime validation on amd64 still
  needs: reloc handler at `mazarin/mazdl/reloc_amd64.go` (apply
  `R_X86_64_{RELATIVE,GLOB_DAT,JUMP_SLOT,64}`) and a container arch
  toggle in the `mazlink-smoke` task so the x86_64 image actually
  runs on an arm64 host. The smoke Dockerfile already cross-builds
  both arches; the `mazlink-smoke` task only runs the container
  matching the host arch today.

**Scope:** ~1500 LOC across mazdl + kernel syscall.

### Phase 5 — migrate existing `.maz` loaders

**Goal:** All current `.maz` callers use `mazdl`. `SysLoadMaz`
deleted.

**Work:**
- Rewrite `mazhost/load.go` around `mazdl.Open`.
- Rewrite disk priest `mazhost` bridge.
- Delete kernel `SysLoadMaz`, `SysRunMaz`, `MazLoadResult`,
  `LoadMazWorkRequest`.
- Delete the `MazarinMain`/`MazarinShepherd`/`PriestInitAddr`
  symbol-name hunt in `kmazarin/ksyscall/loadmaz.go`.
- Retire `maz-reloc` on arm64/amd64; it's only kept for riscv64
  per `GO-PLUGIN-SPIKE.md`.

**Exit criterion:**
1. `$GO tool task run-arm64-hvf TIMEOUT=0` brings up the full
   system (fs.maz + disk + stdio + clocks + mancini) using
   `mazdl` exclusively.
2. Same on amd64.
3. riscv64 still boots via the legacy `.maz`+`maz-reloc` path
   (we haven't touched riscv64 yet).
4. Full response-test and stability-test suites pass on arm64 and
   amd64. riscv64 passes on the legacy path.

**Scope:** ~400 LOC of rewrites + ~800 LOC of deletions. Net
reduction.

### Phase 6 — expand policy list (incremental, per-issue)

**Goal:** Move packages from "per-plugin" to "host-resident" as
real issues surface.

**Work:** Case-by-case. Expect additions for `sync`, `sync/atomic`,
`internal/abi`, `os` (stdio singletons), `time` (timer goroutine).

**Exit criterion:** Per-addition unit test + annotation in the
policy file explaining the trigger.

### Phase 7 — riscv64 backend

**Goal:** Plugin-shape for riscv64. Covered in
`design/GO-PLUGIN-SPIKE.md` Phase 2. Kicks off after Phase 5 has
burnt in on arm64/amd64.

### Phase 8 — `dlclose` and unloading

**Goal:** `h.Close()` actually unmaps the plugin.

**Work:** Reference counting, `DT_FINI_ARRAY` execution,
`munmap`, unregister from `globalSyms`, optionally detect
dangling references (for debug builds).

**Exit criterion:** Repeated Open/Close cycle for 10k iterations
shows bounded VA and heap usage.

**Scope:** ~400 LOC. Defer until someone actually needs to unload
a plugin — today no caller does.

---

## 10. Testing strategy

### Linker-side

Each phase of linker work lands with a Go test under
`mazlink-patches/cmd/link/internal/ld/mazdl_test.go` that:

- Builds a tiny plugin with a hand-crafted source.
- Invokes the patched linker.
- Uses `debug/elf` on the output to assert section presence and
  relocation types.
- Asserts symbol counts (UNDEF count, defined exports).

Run via: `$GO tool task mazlink:test`.

### `mazdl` side

- Unit tests with hand-crafted mini-ELFs under
  `mazarin/mazdl/testdata/` — verify reloc application, symbol
  resolution, error cases (missing symbol, duplicate SONAME,
  bad DT_NEEDED).
- Integration test: `smoke/host` loads `smoke/plugin` and calls
  each exported function. Runs on amd64 and arm64 as part of
  CI-equivalent (`$GO tool task smoke:plugin`).

### End-to-end

The existing `response-test` and `stability-test` harnesses cover
regressions in the real system. After Phase 5, both must pass on
amd64 and arm64 with no baseline regression.

### Plugin size regression guard

Taskfile target that fails if any `.maz` file grows past a
threshold (1 MB post-Phase-2, 2 MB for larger plugins like
`fs.maz`). Prevents silent regressions where a policy miss sneaks
runtime code back into plugins.

---

## 11. Retirement of current workarounds

Post-dlopen, these existing mechanisms become dead code on
arm64/amd64:

| Mechanism | Why it exists today | Why it goes away |
|---|---|---|
| `maz-reloc` thin-stub body trampolines | Plugin has its own `runtime.morestack`, needs redirection to host | Plugin no longer contains `runtime.morestack` — the `.plt` stub goes directly to host |
| `maz-reloc` `.maz_imports` / `.maz_import_strtab` sections | Custom import table because ELF didn't have one | Standard `.dynsym`/`.rela.plt` replace them |
| `syncMazWriteBarriers` | Plugin has its own `runtime.writeBarrier` flag, needs periodic sync with host | GOT-indirected data; host's flag is *the* flag |
| `preGrowStack` | Plugin `morestack` corrupts stacks because it runs against host runtime | No plugin `morestack`; host's own handler runs correctly |
| Kernel symbol-name hunt for `MazarinMain` etc. | No dlsym | `mazdl.Sym` |
| `RegisterMazModuledata` host-side helper | Kernel needs to walk plugin symbols to find `firstmoduledata` | `_mazdl_register_moduledata` init-array entry does it from inside the plugin |

Net effect: the mazlink patch delta *grows* temporarily (Phase 2)
then shrinks (Phase 5) as retirements land. End state: patch
footprint smaller than today for arm64/amd64; larger for riscv64
until Phase 7.

---

## 12. Open questions

- **Deduplicate exports, or error on collision?** MVP errors on
  duplicate symbol across modules. Real glibc does first-loaded-
  wins. Revisit if plugin-to-plugin composition becomes common.
- **`DT_VERSYM` / symbol versioning.** Not in MVP. Needed only if
  we ship plugins built against different Go toolchain versions
  than the host. Defer.
- **TLS (`G` register).** The G register is `R14` (amd64) / `R28`
  (arm64), set by the host runtime. Plugin code reads it
  directly; plugin's call into `runtime.mallocgc` (host's copy)
  finds the same G. Should Just Work. Verify via a unit test that
  loads a plugin and calls `runtime.GoroutineProfile` from within
  plugin code.
- **Plugin-to-plugin calls.** Not supported in MVP. Plugin A that
  wants plugin B's API goes through the host. Revisit if a real
  use case appears.
- **`os.Args`, `os.Environ`.** The plugin gets its own copies at
  load time. If the host mutates these after load, plugin sees
  stale values. Acceptable for MVP; move `os` to host policy
  (Phase 6) if it bites.
- **Stack growth inside plugin code.** Plugin's `runtime.morestack`
  is gone (Phase 2). Calls into host `runtime.morestack` work
  because the G is shared. But the plugin's goroutine stacks are
  allocated by host `runtime.newproc`, which uses host's heap —
  so stack growth hits host's allocator. Should Just Work;
  explicitly test with a deep-recursion plugin function.
- **Go's `panic`/`recover` across module boundaries.** Host-resident
  `runtime.gopanic` walks the defer chain. Defer records are in
  plugin's goroutine stack. `gopanic` walks them the same way
  regardless of where they were pushed. Expected to work; test
  explicitly with a panic across the plugin/host boundary.

---

## 13. Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Hidden runtime symbols that Go marks LOCAL break export | Medium | High — blocks Phase 3 | Exhaustive promotion pass in `ld/mazdl.go`; test catches missing promotions before Phase 4 |
| Policy miss leaves a singleton goroutine duplicated | Medium | High — runtime corruption | `runtime.Stack` assertion in Phase 4 exit criterion; continuous via stability-test |
| `debug/elf` rejection of our plugin format | Low | Low | Custom `mazarin/mazdl/elfread` parser already planned |
| Plugin-to-host call crosses a cross-module-inlined function | Medium | Medium — obscure panics | Expanded policy list (Phase 6) captures `internal/abi`; add per-case |
| Go 1.27 changes runtime symbol set, breaks policy file | Low now, certain eventually | Medium | Policy file is version-pinned to Go toolchain; update as part of Go upgrade work |
| riscv64 development stalls on legacy path while arm64/amd64 moves | Medium | Medium | Keep legacy `.maz`+`maz-reloc` functional through Phase 7; run CI on all three archs every phase |

---

## 14. Deliverables checklist

Tracking-grade list to tick off per phase.

- [x] **Phase 0:** `smoke/host` passes on amd64 and arm64 after `mazlinkNopHostInitTasks` lands (2026-04-18).
- [ ] **Phase 1:** policy file + this doc reviewed and approved.
- [x] **Phase 2:** `nm plugin.maz | grep ' T runtime\.'` returns empty on amd64 and arm64 (2026-04-18).
- [x] **Phase 3:** `smoke/host-probe` exports 3292 `runtime.*` as `GLOBAL DEFAULT FUNC` on arm64; kmazarin/shepherd Taskfile wiring deferred to Phase 4 (2026-04-18).
- [x] **Phase 4 (arm64):** `mazdl.Open` loads `smoke/plugin` successfully; exits #1-#4 green under `$GO tool task mazlink-smoke` with mazlink option A alone — no loader-side workaround (2026-04-18).
- [ ] **Phase 4 (amd64):** same four exits on amd64; mazlink option A is present in `amd64/asm.go` but runtime validation still needs `reloc_amd64.go` + container arch toggle in `mazlink-smoke`.
- [x] **Phase 4 cleanup:** mazlink option A lands on both arches; `rewriteHostFuncvals` removed from `mazdl/open.go` (2026-04-18).
- [ ] **Phase 5:** `SysLoadMaz` deleted; all existing flocks load via `mazdl`; stability-test clean on amd64 + arm64.
- [ ] **Phase 6:** policy file has grown by at most 3 packages beyond Phase-2 starting set without issues justifying each.
- [ ] **Phase 7:** riscv64 plugin-shape lands (separate tracking doc).
- [ ] **Phase 8:** `Close()` actually unloads.
