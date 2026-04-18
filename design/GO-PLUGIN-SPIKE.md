# Go Plugin Spike — Scoping Report (2026-04-17)

## Environment context (non-negotiable)

Mazarin runs Go binaries directly on a custom kernel. There is **no libc**,
**no cgo**, **no C toolchain** anywhere in the userspace we ship. All
flocks build with `CGO_ENABLED=0` and call into Mazarin's kernel via the
Linux-shaped syscall ABI exposed by the linux shepherd. Any plan that adds
a libc dependency or requires cgo in our output binaries is a non-starter.

Any solution has to work inside those constraints.

## RISC-V constraint: no PIE, no plugin-shape, ever (until upstream Go changes)

Before anything else: **Go's toolchain does not emit position-independent
code on `linux/riscv64`.** Not via `-buildmode=pie`, not via
`-buildmode=plugin`, not via any internal-linker path. Confirmed against
Go 1.26.2:

- `InternalLinkPIESupported` (`internal/platform/supported.go:219-228`)
  whitelists `linux/arm64`, `linux/amd64`, `linux/loong64`,
  `linux/ppc64le`, darwin, windows. **`linux/riscv64` is absent.**
- The Go 1.26 release notes mention riscv64 exactly once: "The
  linux/riscv64 port now supports the race detector." Nothing about
  PIE, plugin, GOT emission, or dynamic-ELF support. No upstream
  movement toward adding them.
- `BuildModeSupported` does list `linux/riscv64` under `pie` and
  `plugin`, but only via external linking + cgo — which our
  environment does not have.

Current riscv64 `.maz` builds reflect this constraint. They use
`-ldflags="-T 0x3N000000"` to place an **ET_EXEC** binary at a fixed
slot address (`shared/constants/mzr_slots.go`), then `maz-reloc`
post-processes it. This is structurally distinct from the arm64/amd64
path which produces a genuine ET_DYN PIE.

**Consequence: plugin-shape output (option A's goal) is not reachable
on riscv64 with any stock-Go flag combination.** Closing this gap
requires implementing riscv64 PIE code generation and internal-linker
dynamic-ELF support ourselves — work comparable in scope to what
`mazlink` already absorbs for arm64/amd64 (the three-gap patches
described under option A), plus the per-arch plumbing the internal
linker has never had for riscv64: PLT stub sequences, GOT access
idioms (AUIPC+LD), TLS-model encodings, and the static-to-dynamic
relocation-type mapping.

### Plan: extend `mazlink` to emit riscv64 PIE over time

**riscv64 stays.** We will keep the architecture as a first-class
target and plan to absorb the missing PIE/plugin-shape work into
`mazlink` as a follow-up phase:

- Phase 1 (option A commitment): land `mazlink`'s arm64/amd64
  gap-filling. Plugin-shape becomes the production mechanism on
  those two archs. riscv64 continues on the existing `.maz`
  machinery (ET_EXEC at slot address + `maz-reloc` + thin stubs +
  `syncMazWriteBarriers` + `preGrowStack` + `RegisterMazModuledata`
  + body trampolines). Kernel module loader is dual-mode during
  this phase.
- Phase 2 (later): extend `mazlink` with riscv64 dynamic-ELF
  support — PLT stub emission, GOT layout, TLS-model handling, and
  the full static→dynamic reloc-type mapping for riscv64. Scope is
  larger than the arm64/amd64 gap-fill (probably ~500–1500 LOC of
  per-arch linker code plus tests), but it is bounded per-arch work
  comparable to adding a new architecture backend, not a research
  project.
- Phase 3: once mazlink emits riscv64 plugin-shape, retire the
  `.maz`-custom machinery on riscv64 and the kernel's module loader
  goes back to single-mode.

During Phase 1 the retirements listed later in this document apply to
arm64/amd64 only; riscv64 keeps the legacy mechanism. Retirement
becomes universal at Phase 3, when `mazlink` has grown riscv64 PIE
emission. The Phase 2 work is noted as a known cost we're signing up
for, not a showstopper.

If upstream Go ever adds `linux/riscv64` to `InternalLinkPIESupported`
the Phase 2 work shrinks dramatically — from "implement it" to "port
our three-gap patch to the new backend." No upstream movement is
currently visible, but the door stays open.

## Question

Can we replace the custom `.maz` loader (thin stubs, body trampolines, DCE
prevention, writeBarrier sync, manual moduledata registration) with an
ELF-shared-object mechanism that rides on the dynamic linker's global-scope
rules — so the plugin's internal `runtime.writeBarrier` and friends bind
to the host's copy, eliminating the coherence problem that motivated
`syncMazWriteBarriers`?

## Spike

Built two binaries for linux/arm64 as probes — **not** proposed build modes
for Mazarin, just diagnostic artifacts:

- `pluginpkg/plugin.so` — `go build -buildmode=plugin` with
  `CGO_ENABLED=1`, `CC=aarch64-linux-musl-gcc`, `-fuse-ld=bfd`.
- `pluginpkg/plugin-pie.so` — `go build -buildmode=pie` with `CGO_ENABLED=0`.
- `host/host` — `go build` of a program that imports `plugin`
  (forces `-rdynamic` + cgo).

Enumerated dynamic relocations, symbols, and init hooks in each.

## Findings

### 1. `-buildmode=plugin` delivers exactly the properties we want

| Property                             | Count   |
|--------------------------------------|---------|
| Exported `runtime.*` (GLOBAL DEFAULT)|  1727   |
| Overlap with host's exported runtime |  1725   |
| `R_AARCH64_RELATIVE`                 |  4197   |
| `R_AARCH64_ABS64`                    |  7130   |
| `R_AARCH64_GLOB_DAT`                 |   836   |
| `R_AARCH64_JUMP_SLOT`                |    53   |
| `R_AARCH64_TLS_TPREL`                |     1   |
| UND libc symbols                     |    48   |
| `.init_array` entries                |     2   |

Key mechanisms:

- **GOT-mediated runtime references + default visibility + no `-Bsymbolic`.**
  Plugin-internal references to `runtime.writeBarrier`, `runtime.sched`,
  `runtime.gcphase`, etc. go through GOT slots. When the dynamic linker
  resolves GOT entries with host's `runtime.*` already in the global
  scope, every plugin reference rebinds to the host's copy. 1725 of the
  plugin's 1727 exported runtime symbols are overridden this way.
- **`.init_array` hands moduledata to the host.** Two entries:
  `frame_dummy` (crt no-op) and `go:link.addmoduledata` (Go-linker-
  synthesized wrapper that calls `runtime.plugin_lastmoduleinit` with
  the plugin's `firstmoduledata`). No manual `RegisterMazModuledata` +
  typelinks/itabs walk needed — the Go runtime handles it.
- **BIND_NOW / FLAGS_1:NOW** — no lazy PLT resolver needed; we apply
  every relocation eagerly at load.
- **TLS is one reloc** on `runtime.tls_g`; if host and plugin share
  runtime, they share the TLS slot automatically.

### 2. Plugin mode hard-requires cgo — our environment has none

Go's toolchain refuses to build a plugin without cgo:

```
$ CGO_ENABLED=0 go build -buildmode=plugin .
-buildmode=plugin requires external (cgo) linking, but cgo is not enabled
```

Plugin mode also forces `-linkmode=external`, so even if we worked around
the cgo check we'd need a C compiler to drive the final link. The 48 UND
libc symbols in the plugin output above come directly from `runtime/cgo`'s
bootstrap (`x_cgo_inittls`, signal handling, pthread key setup) — they're
imposed by plugin mode, not by anything in user code.

Net: we cannot use stock `-buildmode=plugin` as-shipped.

### 3. `-buildmode=pie` with `CGO_ENABLED=0` strips too much

Tested `CGO_ENABLED=0 go build -buildmode=pie` on the same package:

| Property                    | plugin (cgo)      | pie (no-cgo)          |
|-----------------------------|-------------------|-----------------------|
| ELF type                    | DYN               | DYN                   |
| Relocation types            | 5                 | **1 (RELATIVE only)** |
| UND libc symbols            | 48                | 0                     |
| Dynsym entries              | 9598              | **~empty**            |
| Exported `runtime.*`        | 1727              | **0**                 |
| `.init_array`               | yes               | **none**              |
| DT_NEEDED                   | libc.so           | none                  |
| Size                        | 6.2 MB            | 2.3 MB                |

PIE gives us "no libc dependency" but removes every single property that
made plugin mode work:

- No `.dynsym` entries → no symbol lookup → host cannot find the
  plugin's entry point, and the plugin's runtime references cannot be
  preempted by host's copy.
- No `.init_array` → no `addmoduledata` hook → the plugin's moduledata
  is never handed to the host runtime.
- Relocations collapse to `R_AARCH64_RELATIVE` only → the plugin is a
  self-contained blob with no mechanism to reach out to the host.

PIE-no-cgo is just a position-independent exec. It's not the shape we need.

### 4. The gap

We need a build output that has the **plugin-mode shape** (default-visibility
runtime exports, GOT-mediated runtime refs, `.init_array` addmoduledata,
no `-Bsymbolic`) but **without the cgo baggage** (no UND libc, no
`runtime/cgo` dependency, usable with Go's internal linker). The Go
toolchain as shipped produces one or the other but not both.

## Side-by-side comparison (for the mechanism, independent of how we get it)

| Concern                         | `.maz` (today)                                | Plugin-shape .so (goal)                      |
|---------------------------------|-----------------------------------------------|----------------------------------------------|
| Runtime symbol sharing          | Body-trampolined per-symbol (manual list)     | ELF global-scope preemption via GOT          |
| DCE prevention                  | Force-reference every runtime symbol in host  | Host emits full `.dynsym` (plugin-mode host) |
| writeBarrier / gcphase sync     | `syncMazWriteBarriers` + manual registration  | Same memory — nothing to sync                |
| Module registration             | `RegisterMazModuledata` + typelinks + itabs   | `.init_array` → `plugin_lastmoduleinit`      |
| Type dedup                      | `buildCompleteTypemap` manual method walk     | Go runtime handles via normal plugin flow    |
| Init task filtering             | Skip `runtime.*` init tasks by prefix         | Not needed — plugin has no duplicate runtime inits (fixed by linker) |
| Stack growth                    | `preGrowStack` workaround (.maz morestack)    | Plugin uses host morestack (global scope)    |
| Symbol lookup                   | SymbolTable + manual name matching            | dynsym lookup via plugin ptab                |
| Assembly ABI bridging           | Custom body trampolines for morestack/wbBuf   | None — same code addresses                   |

Every manual workaround on the left falls out of the dynamic linker's
semantics — **if** we can emit plugin-shape output.

## What a "normal" Linux Go setup does

A ~1-day code-read of `cmd/link/internal/ld/` plus testing
`-linkmode=external` with `CGO_ENABLED=0` revealed the actual build
pipeline for `go build -buildmode=plugin` on stock Linux, and it
clarifies why the three gaps exist.

```
.go sources
    │
    ▼
┌──────────────────────┐
│ Go compiler          │  writes Go-native object format
│ (go tool compile)    │  (`go object linux …` magic, not ELF)
└──────────────────────┘
    │  _pkg_.a  (ar archive containing _go_.o, Go format)
    ▼
┌──────────────────────┐
│ Go internal linker   │  combines all Go packages into
│ (cmd/link)           │  ONE standard ELF relocatable .o
└──────────────────────┘
    │  go.o  (standard ELF64, ET_REL)
    ▼
┌──────────────────────┐
│ External linker      │  receives Go's relocatable .o plus
│ (gcc → ld / lld)     │  C runtime startup (crti.o/crtn.o,
│                      │  crtbegin.o/crtend.o), cgo's
│                      │  libpthread/libc references
└──────────────────────┘
    │
    ▼
  plugin.so  (ET_DYN with .dynsym/.init_array/GOT/PLT/…)
```

**Who fills the three gaps in the "normal" flow:**

1. **`.init_array` entry that calls `plugin_lastmoduleinit`:** Go's
   internal linker emits a symbol named `go:link.addmoduledata` (a
   Go function) and *marks it as an init-array constructor*. The
   external linker's standard `__attribute__((constructor))`/crt
   machinery places the symbol's address into `.init_array` during
   final link. The glue is the external linker's C-startup code
   that Go's side doesn't duplicate.

2. **Runtime symbols in `.dynsym`:** Go passes `-rdynamic` (or
   equivalent) to the external linker when building a plugin. `ld`'s
   `-rdynamic` means "put all globally-visible symbols into the
   dynamic symbol table." The external linker iterates Go's `.o`
   and exports everything with GLOBAL DEFAULT visibility — that's
   where the 1727 runtime exports come from. Go's internal linker
   has no equivalent iteration because it has always relied on
   `ld` to do it.

3. **JUMP_SLOT / TLS_TPREL relocations:** These come out of the
   external linker's handling of the cgo bootstrap (the
   `runtime/cgo` C code that pthread_creates worker threads, sets
   up TLS keys, etc.). The relocations point at libc/libpthread
   symbols that the cgo stub references. Go's internal linker
   emits relocation *requests* for these symbols; `ld` produces
   the actual `.rela.plt` / TLS entries.

**So the "normal" answer is: the external linker (`ld`/`lld`) does all
three jobs, and cgo is how Go gets those jobs onto the external
linker's work list.** The Go internal linker was never taught to do
them because real Linux always has `ld`. The three-gap code we'd have
to write is code the internal linker has no reason to own in upstream
Go.

That is exactly why option A is ~200 lines rather than 20: we're not
removing gates on code the internal linker already runs — we're
porting three small responsibilities from the external linker *into*
the internal linker, because we don't have an external linker.

## Paths to close the gap

Four options, ranked by how much we'd have to build:

### Option A — Patch Go's internal linker to emit plugin-shape without cgo

A code-read of `cmd/link/internal/ld/` (2026-04-17) answers the critical
question: the internal linker has *most* of the plumbing for plugin-shape
output, but three specific pieces of work are delegated to the external
linker today.

**What's already there (internal linker):**

- `.dynsym`/`.dynstr`/`.dynamic`/`.got`/`.plt` emission paths exist
  (`elf.go:1467-1596`), but are suppressed when `ctxt.IsExternal()` is
  true (line 1463).
- `.init_array` *section* is allocated for plugin, c-shared, shared,
  c-archive build modes (`data.go:1981-1984`).
- `R_AARCH64_GLOB_DAT` emission via `AddGotSym()` is implemented
  (`arm64/asm.go:138`).
- GOT slot allocation for runtime symbols is in place.
- ET_DYN output works — proven by `-buildmode=pie`, CGO_ENABLED=0 going
  through the internal linker and producing a valid (if minimal)
  position-independent ELF.

**What's missing (three gaps):**

1. **`go:link.addmoduledata` wrapper synthesis.** The symbol that goes
   into `.init_array` and calls `runtime.plugin_lastmoduleinit(&firstmoduledata)`
   is generated via the external linker's path today. The internal
   linker creates the `.init_array` section but does not populate its
   entry. **~40-60 lines** to synthesize the wrapper and enter it.

2. **Automatic runtime-symbol export to `.dynsym`.** `Adddynsym()`
   (`go.go:365`) early-returns on `LinkMode == LinkExternal`. More
   fundamentally, the decision *which* 1700+ runtime symbols become
   GLOBAL DEFAULT exports is implicit in the external linker's
   `-rdynamic` handling today — the internal linker has no equivalent
   iteration over the runtime and export list. **~30-50 lines** to
   enumerate and export.

3. **Full arm64 dynamic relocation coverage.** `R_AARCH64_GLOB_DAT`
   works; `R_AARCH64_JUMP_SLOT` and `R_AARCH64_TLS_TPREL` emission
   paths are either absent or only reachable via external-linker
   handoff. **~50-80 lines** to complete.

Plus ~20 lines of gate removals / mode-decision tweaks in
`config.go:157` and `lib.go`, and the `runtime/cgo` dependency
short-circuit in `cmd/go/internal/load/pkg.go:2643-2648`.

**Estimate: ~150-250 lines of internal-linker work**, plus the gating
checks, plus ~30-50 lines for the DCE/plugin-reachability flag (see
"What keeps runtime symbols alive?" below). Total ~200-300 lines. Not
a one-line patch, but not a rewrite either. The plumbing is present;
we're finishing three specific code paths.

Pros: smallest change that stays Go-only; tracks upstream Go cleanly
via our existing `runtime-patches/` overlay pattern; no libc, no cgo,
no external C toolchain anywhere in the shipping pipeline; plugin
output shape is identical to what stock plugin mode produces, so the
Mazarin-side ELF loader is the same loader that would consume any
real Go plugin.

Cons: ongoing patch maintenance (three linker source files plus
`cmd/go` in each Go release); we become the only user of
"plugin-mode with internal linkmode" and will hit bugs nobody else
sees; the ~200 lines of new linker code sits in our fork rather than
upstream (a PR to upstream is possible but unlikely to land — the
Go team has no motivation to unwind plugin-mode's external-linker
assumption).

### Option B — Post-process a `-buildmode=pie` output into plugin shape

Start with `CGO_ENABLED=0 -buildmode=pie` (which we know builds cleanly).
Write a tool that, given the pie `.so` and the Go linker's intermediate
symbol table output (`-dumpdep` or similar), synthesizes the missing
plugin-mode artifacts:

- Populate `.dynsym`/`.dynstr` from the full symbol table.
- Add `.init_array` with an entry that calls a synthesized
  `addmoduledata` stub (we can write that in Go and linkname-export it).
- Convert direct `runtime.*` references to GOT-indirected loads (this is
  the hard part — it means rewriting code, not just adding metadata).

Pros: no Go toolchain patch; entirely our tooling.

Cons: the GOT-indirection conversion is essentially reimplementing a
chunk of the Go linker's relocation emission. High complexity, brittle.
We've been burned by `cmd/maz-reloc` already; doubling down on that
pattern isn't appealing.

### Option D — Use gaston as our cgo toolchain, satisfy plugin mode on its own terms

This option emerged once we re-examined *why* plugin mode demands cgo: it's a
transitive consequence of external linking (plugin → `-linkmode=external` →
`runtime/cgo` → libc), not an architectural requirement of the plugin output
shape itself. We already own a C compiler — `gaston` at
`/Users/iansmith/gaston` — with a libc we fully control. Instead of fighting
Go's linker to skip cgo (option A), we can satisfy the cgo-import on our own
terms by extending gaston's libc with the exact surface `runtime/cgo`
needs, routed to Mazarin primitives.

**What `runtime/cgo` actually imports (linux/arm64).** The cgo bootstrap
is `runtime/cgo/gcc_linux_arm64.c` (~90 lines) plus the common C files.
The surface is roughly 45 libc symbols in 6 clusters:

| Cluster             | Symbols                                                          | Mazarin mapping                              |
|---------------------|------------------------------------------------------------------|----------------------------------------------|
| pthread thread      | `pthread_create`, `pthread_attr_{init,destroy,getstacksize,setstacksize}`, `pthread_detach`, `pthread_sigmask` | see pthread section below — mostly bypassed |
| pthread TLS keys    | `pthread_key_create`, `pthread_setspecific`, `pthread_getspecific` | Trivial thread-local map; we already manage TLS |
| signals             | `sigfillset`, `sigemptyset`, `sigaction`, `pthread_sigmask`      | We don't deliver POSIX signals; most become no-ops |
| malloc/free         | `malloc`, `calloc`, `free`, `realloc`                            | Wrap kernel `mmap`/`munmap`; cgo allocations are rare |
| errno/strerror      | `__errno_location`, `strerror`                                   | Thread-local int + static table              |
| stdio (diagnostics) | `fprintf`/`fputs`/`abort`/`_exit`/`write`                        | gaston already has most of this              |

Gaston's current libc (`/Users/iansmith/gaston/libc/`) provides
`errno.c`, `printf.c`, `sscanf.c`, `stdio.c`, `setjmp_arm64.s`,
`mathbuiltins.c` and the usual headers — 12 files total. The gaps are
pthread, signals, and mmap-backed malloc. The pthread analysis below
shows the pthread gap is smaller than it looks.

**Pros:** gaston is ours; we ship it; extending its libc for one specific
consumer (`runtime/cgo`) with one specific target OS (Mazarin-linux) is
well-scoped. No upstream Go patches. The cgo runtime can stay intact —
which means if we ever want real-world cgo packages later, the
infrastructure is there.

**Cons:** we take on libc ownership for an in-process Go runtime dependency,
not just a handful of user programs. A bug in our malloc now shows up as
heap corruption in Go. Also, `runtime/cgo` is a C↔Go bridge designed
around glibc/musl assumptions; every deviation (TLS model, signal model,
thread lifecycle) is a potential impedance mismatch that we absorb.

### Option C — Abandon plugin-mode and stay with .maz, fix the write-barrier bug directly

If the spike shows option A is hard, the alternative is to resume the
write-barrier investigation and/or pursue task #19 (`[]ot.Segment` GC
bitmap after typemap merge). We already have `syncMazWriteBarriers`
instrumented; adding a `slice.go` overlay with growslice entry logging
would reveal whether the cap field is corrupt on entry or whether
growslice itself is misbehaving.

Pros: no new mechanism; focused debug of an existing system.

Cons: doesn't address the structural fragility of .maz (thin stubs,
typemap merging, writeBarrier sync, preGrowStack). Each of those is
a plausible source of the next bug after this one.

## What keeps runtime symbols alive? (DCE and inlining)

Plugin-shape preemption only works if the host binary actually contains
and exports the runtime symbols the plugin will bind against. Two
separate mechanisms could strip them: Go's linker does aggressive dead-
code elimination, and the Go compiler inlines small functions. Both
need to be neutralized for the runtime symbols we care about.

### Inlining: less of a problem than it looks

- The runtime state that matters for GC coherence
  (`runtime.writeBarrier`, `runtime.sched`, `runtime.gcphase`,
  `runtime.memstats`) are **global variables**. Variables don't inline.
  References to them become GOT-indirected loads in plugin-shape
  output, which is exactly the preemption point we want.
- Inlined runtime **functions** still get their out-of-line body
  emitted as a named symbol unless the compiler can prove no external
  caller exists. For exported `runtime.*` symbols that proof never
  holds — the body is always there.
- Assembly-implemented entry points (`runtime.gcWriteBarrier{1..8}`,
  `runtime.morestack`, `runtime.newproc`, the `runtime.asyncPreempt*`
  family) are not inlineable at all.
- Inlining on the plugin side does not "capture" the plugin's copy
  of a runtime symbol. The compiled code is a GOT-indirected load;
  at load time the GOT resolves to the host's address. Inlining and
  GOT preemption are orthogonal.

### DCE: real concern, addressed by two cooperating knobs

Stock Go's plugin story solves DCE through a pattern we need to
replicate:

1. **`CanUsePlugins()` linker flag.** When the linker detects a
   plugin-capable host (today: the host imports `"plugin"`), DCE is
   **disabled on the runtime**. Every runtime symbol is marked
   reachable from the start, independent of whether host code
   references it. Without this flag, DCE would strip the large
   fraction of the runtime the host happens not to exercise directly.
2. **The `plugin` package pins runtime internals.**
   `runtime/plugin.go` and `plugin.Open` reference
   `firstmoduledata`, `typelinks`, `itabsinit`, `moduledataverify`,
   and the module-list walk via `//go:linkname`. Importing `plugin`
   pulls these into the reachability closure.

### What we need in Mazarin

- A **host-side build flag** (or equivalent, piggy-backing on
  `CanUsePlugins()`) that our patched linker honors: when set,
  disable runtime DCE and emit the full runtime export list to
  `.dynsym`. ~30-50 lines of linker work on top of the three
  internal-linker gaps.
- A **pure-Go `mazarin/plugin` package** that has linkname
  references to the runtime internals our Mazarin plugin loader
  will need (moduledata walks, typelink consumption, etc.).
  Importing this package is how a flock host opts into "I will
  load plugins; keep the runtime's plugin-support internals alive."

### The nice side effect

Today `.maz` has the same structural problem — loading a .maz
module that uses an interface requires the host to have force-referenced
that interface's concrete type, or the linker strips the type from
the host's typelinks. This is the `forceBlockDevItab(blkDev)` pattern
we've already had to write for `priest.(blockdev.BlockDevice)` and
similar cases. Under plugin-shape with `CanUsePlugins()` + the
`mazarin/plugin` import, that reachability pinning happens
**structurally** — no per-interface force-reference boilerplate, no
`//go:noinline` hacks, no "you forgot to force-reference X and now
itab lookup returns nil" bug class.

One more ongoing maintenance tax we get to drop.

## Who actually calls pthread_create?

This question is load-bearing for option D, because the pthread cluster
looks like the scariest part of the cgo surface. We already implement
`clone(2)` in the kernel for the Go runtime — so who, in a cgo-linked
binary, is asking the libc to call `pthread_create`?

The answer is one function: `runtime.newm1` at `runtime/proc.go:2906`.

```go
func newm1(mp *m) {
    if iscgo {
        var ts cgothreadstart
        if _cgo_thread_start == nil { throw("_cgo_thread_start missing") }
        ts.g.set(mp.g0)
        ts.tls = (*uint64)(unsafe.Pointer(&mp.tls[0]))
        ts.fn = unsafe.Pointer(abi.FuncPCABI0(mstart))
        execLock.rlock()
        asmcgocall(_cgo_thread_start, unsafe.Pointer(&ts))
        execLock.runlock()
        return
    }
    execLock.rlock()
    newosproc(mp)
    execLock.runlock()
}
```

One branch. Two paths:

- **cgo path:** dispatch to `_cgo_thread_start`, a function pointer filled
  in by `runtime/cgo` at init time. Its Linux implementation
  (`runtime/cgo/gcc_linux_arm64.c`) calls `pthread_create`, and the
  pthread-created OS thread eventually lands back in Go's `mstart`.
- **non-cgo path:** call `newosproc(mp)` at `runtime/os_linux.go:170`,
  which invokes `clone(2)` directly with our own stack and entry point.

Which branch runs is determined by the single boolean `runtime.iscgo`,
flipped by a single linkname in `runtime/cgo/iscgo.go`:

```go
//go:linkname _iscgo runtime.iscgo
var _iscgo bool = true
```

Importing `runtime/cgo` sets this; not importing it leaves it `false`.

**Why does cgo prefer pthread_create over clone?** Coexistence with a
hosting libc. In a typical cgo binary, libc's own threading (its pthread
mutexes, TLS destructors, malloc arena locks, signal delivery model)
needs to be initialized on every thread that will ever run C code. The
glibc/musl pthread_create path does that initialization; a raw clone()
does not. If a Go program calls into a C library from a thread that was
born via clone() rather than pthread_create, subtle breakage follows
(uninitialized TLS slots, incorrect signal masks, broken fork handlers).

That entire concern is about interoperating with glibc/musl. We have
neither. Mazarin's "libc" exists only to satisfy `runtime/cgo` itself;
there is no second C-library consumer whose thread-local state we need
to bootstrap. So we do not need to go through pthread_create — we need
`runtime/cgo` to think it did.

**The shortcut for option D.** Under option D, gaston's libc does not
have to implement real POSIX threads. We override `_cgo_sys_thread_start`
(the function `runtime/cgo` calls to actually create the thread) with
a Mazarin implementation that:

1. Takes the `cgothreadstart` descriptor Go hands us.
2. Calls Mazarin's existing kernel clone primitive (`CloneThread` — the
   same one the non-cgo path uses via `newosproc`).
3. Arranges for the new thread to jump to `mstart` with the Go-runtime-
   expected register/TLS state.

That collapses the entire pthread cluster to a few trivial stubs that
`runtime/cgo`'s C code happens to call at init time but that never do
real work:

- `pthread_attr_init` / `pthread_attr_destroy` — return 0.
- `pthread_attr_getstacksize` / `pthread_attr_setstacksize` — store in
  an `int` field, return 0.
- `pthread_sigmask` — no-op (Mazarin's signal model isn't POSIX).
- `pthread_detach` — no-op (we don't track joinable state).
- `pthread_key_create` / `pthread_setspecific` / `pthread_getspecific`
  — small TLS-key table implemented on top of our existing per-thread
  storage. Only used by `runtime/cgo`'s own init and goroutine-to-M
  pinning; volume is low.

That is roughly 5–6 trivial wrappers plus one substantive TLS-key
implementation, rather than a full pthread port. And
`_cgo_sys_thread_start` itself — the one function that actually matters
— lives in our tree, not in gaston's libc, and calls the same kernel
primitive the non-cgo path uses.

**Revised estimate for option D.** With the pthread shortcut:

- ~200–300 lines of gaston libc wrappers (malloc over mmap, sigaction
  no-ops, errno/strerror, stdio fill-ins).
- ~100 lines for the TLS-key implementation.
- ~50 lines for the `_cgo_sys_thread_start` override that re-uses
  Mazarin's existing clone path.
- No pthread port.

Total: substantially smaller than the initial "~500–800 lines" estimate,
and the high-risk parts (threading, TLS) are bypassed rather than
reimplemented.

## Recommended next step

**Commit to option A, in two phases.**

- **Phase 1 — arm64 + amd64.** Land `mazlink` with the three-gap
  internal-linker patches (~200-300 lines per the code-read):
  `.init_array` wrapper synthesis, runtime-symbol export to
  `.dynsym`, JUMP_SLOT/TLS_TPREL emission, plus the DCE/plugin-
  reachability flag and the `cmd/go` short-circuit that removes the
  cgo dependency on plugin-mode. Cutover the `maz/` tree on these
  two archs to plugin-shape `.maz` outputs. Retire the mechanisms
  listed under "Retirements" for arm64/amd64. Kernel module loader
  becomes dual-mode: plugin-shape on arm64/amd64, legacy `.maz`
  on riscv64.
- **Phase 2 — riscv64 PIE in mazlink (later).** Absorb the upstream
  gap: emit riscv64 PLT stubs, GOT layout, TLS-model relocations,
  and the full static→dynamic reloc-type mapping. Larger than
  Phase 1 (probably ~500–1500 LOC of per-arch linker code plus
  tests), but bounded. Once landed, riscv64 `.maz` outputs switch
  to plugin-shape and the remaining retirements become universal.

This is a medium-term commitment — Phase 1 is weeks of real
engineering, Phase 2 likely more — but it closes out a structural
fragility (the .maz write-barrier/morestack/typemap class of bugs)
rather than patching individual symptoms. Option C remains the
fallback if Phase 1 runs into unexpected obstacles, but it is no
longer the recommended path.

### Phase 1 prerequisite

Before writing any linker code: **decide whether to keep riscv64
on the legacy `.maz` machinery or pursue option C in parallel on
riscv64** while arm64/amd64 migrate. Either is defensible:

- **Keep riscv64 on legacy** — minimum engineering; accept that the
  write-barrier and morestack fragilities continue to exist on that
  arch until Phase 2 lands. Best if the CFF bug can be worked around
  or isn't triggered on riscv64 in practice.
- **Fix .maz directly on riscv64 too** — resume the write-barrier
  investigation (task #19: `[]ot.Segment` GC bitmap after typemap
  merge) as part of Phase 1. Keeps riscv64 healthy during the
  dual-mode period. More work, less structural debt.

This prerequisite decision is separate from the A-vs-C framing and
should be made once Phase 1 is actually about to start.

### Option D, B, and C status

- **Option D** (gaston libc + cgo) — off the table. The cgo/libc
  surface is philosophically wrong for a Go-only OS, and option A
  gets us to the same place without introducing cgo anywhere.
- **Option B** (post-process a PIE output) — remains on the shelf
  as a fallback if Phase 1 runs into a Go-linker obstacle that
  makes direct internal-linker patches impractical.
- **Option C** (fix .maz directly) — fallback for the overall
  migration. If Phase 1 estimates turn out badly wrong, or if the
  CFF bug can be pinpointed and fixed in the current .maz model
  with less effort than anticipated, the migration can be deferred
  or cancelled and we continue on the current mechanism indefinitely.

The original implementation-spike proposal (build a plugin, write a
dlopen-clone, run CFF glyph test) remains **premature** — it assumed
we could produce plugin-shape output in our environment with the
stock toolchain. Phase 1 of option A is what unblocks that spike.

## Target architecture: one shepherd, everything is a .maz

**This section describes the Phase 1 target on arm64 and amd64.**
During Phase 1, riscv64 continues using the current `.maz` mechanism
(ET_EXEC at slot address + `maz-reloc`). At Phase 2, when `mazlink`
gains riscv64 PIE emission, riscv64 joins the plugin-shape world and
this section applies universally. The directory layout (`maz/`),
file extension (`.maz`), and shepherd-bootstrap model are uniform
across all archs from the start; only the internal format of a
`.maz` file differs per arch during the dual-mode phase.

With plugin-shape output in hand, the arm64/amd64 userspace
architecture collapses to a single simple shape:

- **One shepherd binary.** The loader runtime. Built with the userspace
  overlay set (runtime/syscall adaptations for the Mazarin environment).
  This is the *only* native Go binary we ship for userspace.
- **One bootstrap plugin per shepherd process.** The shepherd takes a
  `.maz` path as command-line argument, loads that plugin, and hands
  control to its entry point. That plugin may — entirely at its own
  discretion — call `mazarin/plugin.Open()` to pull in additional
  plugins in the same process. The shepherd does not impose a
  one-plugin-per-process rule; it just bootstraps one and gets out
  of the way.
- **Everything else is a plugin.** What we currently call "shepherds"
  (`linux`, `linux-ui`, etc.) and "flocks" (`mail-ui`, `rachel`,
  `fontsvc`, `fs`, `prefs`, …) collapse into the same category:
  bootstrap plugins. The runtime distinction "shepherd binary vs.
  flock binary" disappears; they are all `.maz` files loaded by the
  one shepherd.

This is the JVM pattern. `java -cp ... Main` — one `java` binary, one
main class on the command line, that class is free to load more classes
as it runs.

### Directory layout: `maz/`

Source organization follows the runtime reality. The current split
between `maz/<name>/` (flock sources) and
`mazarin/mazhost/…` / kernel-side plumbing goes away in favor of a
single top-level directory:

```
maz/
  linux/          # today's maz/linux — Linux-ABI bootstrap plugin
  linux-ui/       # today's maz/linux-ui
  mail-ui/        # today's maz/mail-ui
  fs/             # today's maz/fs
  rachel/         # today's maz/rachel
  fontsvc/        # today's maz/fontsvc
  prefs/          # today's maz/prefs
  clocks/         # today's maz/clocks
  … (one subdirectory per plugin)
```

Every subdirectory produces one compiled plugin. No distinction
between "shepherd" sources and "flock" sources at the filesystem
level — because there is no such distinction in the runtime either.

### File extension: `.maz`

Compiled plugin outputs are named `name.maz`, not `name.so`.
Two reasons:

1. **Visual clarity.** A `.maz` extension unambiguously marks a binary
   as "built for Mazarin, by `mazlink`, with plugin-shape output."
   A `.so` would be confusable with a Linux shared library (and most
   Linux `.so` files cannot be loaded by our ELF loader since they
   have libc `DT_NEEDED` entries and non-plugin init structure).
2. **`.gitignore` tractability.** A single line — `*.maz` — covers
   every plugin build output across the whole tree. Mixing outputs
   with Linux-native `.so` files would make the ignore pattern
   incorrect (it would also hide incidental Linux shared libraries
   we might reference elsewhere in the repo).

The `.maz` extension we already use for the current `.maz` module
format carries over unchanged. The format *inside* the file changes
from "our custom PIE ELF with trampolines" to "plugin-shape ELF .so
with Mazarin semantics", but the extension, the directory structure,
and the loader entry points (`mazarin/plugin.Open`) stay the same.

### `mazlink` — the patched-linker tool

The internal-linker patches from option A ship as a standalone tool:

- **Source:** `cmd/mazlink/` in our repo — essentially vendored
  copies of `cmd/link/internal/ld/config.go`,
  `cmd/link/internal/ld/elf.go`, `cmd/link/internal/ld/go.go`,
  `cmd/link/internal/arm64/asm.go` with our gap-filling patches,
  plus a thin main that re-exports `cmd/link`'s main.
- **Build:** built once via `$GO tool task bootstrap-mazlink`
  using stock Go. Resulting binary cached under `bin/mazlink`.
- **Invocation:** the Mazarin build (Taskfile targets that produce
  `.maz` outputs) invokes Go's normal compile pipeline but passes
  `-toolexec` (or installs into a shadow toolchain dir) so that
  `link` calls get routed to `mazlink` instead of stock
  `$GOROOT/pkg/tool/$GOOS_$GOARCH/link`.
- **Scope:** `mazlink` is only invoked for Mazarin plugin builds.
  Any regular `go build` in the repo (host tools, cmd/* helpers)
  continues to use stock `link`. The patched path is strictly
  opt-in per build target.

Onboarding for a developer who "wants to try it":

1. Install stock Go.
2. Clone the repo.
3. `$GO tool task` — builds `mazlink` on first run (small, cached),
   then proceeds with kernel + diplomat + shepherd + plugins as normal.

No Go fork to install. No Homebrew tap. `mazlink` lives beside the
other tools we already ship via `go tool` (`maz-reloc`, `fix-go-elf`,
`mkesp`, `safe-serial-read`, etc.) — it's just one more.

### What goes away in the reorganization

Deletions, not just retirements-of-mechanism:

- `flock/` top-level directory → renamed to `maz/`.
- `maz/shepherd2/`, `maz/shepherd3/`, `maz/shepherdsieve/`,
  `maz/sievetest/` — stale iterations of the shepherd experiment,
  obsoleted by the one-shepherd architecture.
- Per-flock Taskfile targets that apply the overlay set individually
  — replaced by a single `maz` target that builds every subdirectory
  as a plugin with `mazlink`.

### What the shepherd actually is

Mechanically, the shepherd is:

1. A small `main()` that parses argv, opens the argv[1] `.maz`
   via the ELF loader, looks up the plugin's entry point, and
   jumps to it.
2. The userspace overlay set baked in (syscall dispatch, scheduler
   hooks, netpoll init, walltime, cgo_mmap stub, …) — so that when
   a plugin's runtime references preempt to the shepherd, they land
   on overlay-patched implementations.
3. An import of `mazarin/plugin` (for the `CanUsePlugins()`
   reachability pinning) so the shepherd's `.dynsym` exports the
   full runtime surface plugins need.
4. An import of any package a bootstrap plugin might need but the
   Go compiler could otherwise DCE out of the shepherd's
   reachability closure (e.g., rare runtime helpers, reflection
   internals). Each such import is documented with a `Why:` comment
   since they exist purely for symbol retention.

Item 4 is the only hand-maintained knob. Everything else is
structural.

## What the ELF loader in Mazarin would still look like (A or D)

The Mazarin-side work is the same under either A or D — both produce
plugin-shape output, they differ only in whether `runtime/cgo` is
present in the plugin or not (and therefore whether the gaston-libc
symbols need to resolve):

- **ELF dynamic loader** (userspace Go, ~500 lines):
  parse `PT_DYNAMIC`, map `PT_LOAD`, apply the 5 relocation types,
  execute `INIT_ARRAY`, expose dynsym-based lookup. Symbol resolver
  consults host's own dynsym (host built with plugin-support rdynamic)
  before plugin's own symbol table.
- **Host's own dynsym exposure.** The host binary must export its
  runtime symbols in its `.dynsym` so the loader's resolver can see
  them. In stock Go this happens via plugin-support `-rdynamic`; in
  our patched toolchain we'd arrange the same thing without cgo.
- **TLS handling.** One `R_AARCH64_TLS_TPREL` on `runtime.tls_g`,
  resolved to the host's TLS offset so host and plugin share `g`.
- **Host-side plugin open API.** A pure-Go `mazarin/plugin` package
  replacing stdlib `plugin` (which is cgo-only), exposing
  `Open(path) *Plugin` and `(*Plugin).Lookup(name)`. Under the hood
  it calls the loader above and, after `init_array` has run,
  returns an object backed by the plugin's dynsym.
- **Libc shims (option D only).** Under A the plugin has no UND libc
  symbols and no shims are needed. Under D the plugin's UND libc
  symbols resolve against gaston's libc, which is either statically
  linked into the host or loaded as a separate shared object before
  the plugin. The substantive implementations
  (`_cgo_sys_thread_start`, TLS-key table, mmap-backed malloc) live
  in our tree; gaston's libc fills in the wrapper stubs and stdio.
- **`mazarin/plugin` package.** Pure-Go replacement for stdlib
  `plugin` (which is cgo-only). Exposes `Open(path) *Plugin` and
  `(*Plugin).Lookup(name)`; under the hood it calls the ELF loader
  above. Also carries `//go:linkname` references to the runtime
  internals the loader needs (moduledata walk, typelink/itab
  registration) so that importing this package keeps those symbols
  reachable in the host — see "What keeps runtime symbols alive?"
  above. Flock hosts import it to opt into plugin loading.

## Retirements (if plugin-shape works out)

**Phase 1 scope: arm64 and amd64 only.** During Phase 1, every
mechanism retirement below stays active on riscv64 and the kernel's
module loader supports both paths.

**Phase 2 scope: universal.** Once `mazlink` emits riscv64 PIE, the
retirements apply across all three arches and the kernel module
loader returns to single-mode.

Mechanism retirements (Phase 1 = arm64/amd64, Phase 2 = all archs):

- `mazarin/overlay/userspace/runtime/maz_moduledata.go` — Phase 1
  on arm64/amd64. Needed on riscv64 until Phase 2.
- `cmd/maz-reloc` — Phase 1 on arm64/amd64. Needed on riscv64
  through Phase 1 for every existing job (text call-site patching,
  data-pointer patching, runtime-asm body trampolines, unreached-
  stub trampolines, `.maz_imports` section emission). Retires
  universally at Phase 2.
- `build/thin-overlay/runtime/*`, `build/shepherd-overlay/runtime/*`
  — Phase 1 on arm64/amd64, universal at Phase 2.
- `preGrowStack` hacks in `maz/*` (currently `maz/*`) —
  Phase 1 on arm64/amd64. On riscv64 the `.maz`-local morestack
  problem remains until Phase 2.
- `RegisterMazWriteBarrier` / `syncMazWriteBarriers` — Phase 1 on
  arm64/amd64, universal at Phase 2.
- `buildCompleteTypemap` et al. — Phase 1 on arm64/amd64, universal
  at Phase 2.
- `forceBlockDevItab` and similar force-reference patterns in host
  binaries — Phase 1 on arm64/amd64, universal at Phase 2 (replaced
  by `CanUsePlugins()` + `mazarin/plugin` import making reachability
  structural).
- `.mzr` format (ET_EXEC custom modules) — subsumed by plugin-shape
  `.maz` on arm64/amd64 at Phase 1. On riscv64 the current `.maz`
  is already ET_EXEC-at-slot, which is essentially what `.mzr`
  became — so `.mzr` as a *separate* format retires universally at
  Phase 1, but the underlying mechanism stays active on riscv64
  until Phase 2.
- The custom `.maz` PIE-with-trampolines internal format — Phase 1
  on arm64/amd64. Replaced by plugin-shape ELF `.so` semantics on
  those archs. riscv64 joins at Phase 2.

Directory / source-tree retirements (universal, independent of
plugin-shape):

- `flock/` top-level → renamed `maz/`; every `maz/<name>/`
  becomes `maz/<name>/`.
- `maz/shepherd2/`, `maz/shepherd3/`, `maz/shepherdsieve/`,
  `maz/sievetest/` — stale iterations, deleted outright. The one
  "shepherd" under the new architecture is a single minimal loader
  binary, built from a single source location.

Standalone deletion candidates (unrelated to plugin-shape):

- `cmd/pe-fixup` — verified unused in any Taskfile (only references
  are its own source and a mention in `site/quickstart.md`). Check
  whether `elf2pe` now handles the PE subsystem field before
  deleting, but on current evidence `pe-fixup` is dead code.

## Appendix — raw data

- Plugin UND symbols: 48 (all cgo-imposed libc).
- Plugin runtime exports: `/tmp/plugin-spike/plugin-runtime-exports.txt` (1727).
- Host runtime exports: `/tmp/plugin-spike/host-runtime-exports.txt` (1886).
- Preemption overlap: 1725.
- Plugin `.init_array`: `frame_dummy` + `go:link.addmoduledata`.
- Plugin dynamic flags: `BIND_NOW | FLAGS_1:NOW`.
- PIE-no-cgo baseline: single reloc type, empty dynsym, no init_array,
  no DT_NEEDED.
