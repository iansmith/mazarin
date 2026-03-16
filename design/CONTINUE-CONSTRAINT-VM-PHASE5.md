# Continue: Constraint VM Phase 5 — Client-Side Library

## Context

Branch: `feature/constraint-vm`
Strategic plan: `design/constraint-vm-strategic-plan.md` (Phase 5, lines 131-152)

Phases 1-4 of the Constraint VM are complete and committed on `feature/constraint-vm`:

- **Phase 1** (`mazarin/vm/flat/`): Flat data representation — FlatValue (32 bytes,
  pointer-free), FlatAttrNode (128 bytes), PageRegion allocator, 21 type tags,
  composite types (Timespec, Rectangle, Point2D/3D, IPv4/6, PriestId, MazId, etc.)
- **Phase 2** (merged into Phase 3): Shared page allocation for constraint data.
- **Phase 3** (`kmazarin/ksyscall/constraint_*.go`): Kernel attribute management —
  6 syscalls (0x1021-0x1026), namespace trie with seqlock, dirty propagation via
  reverse edges, query result attributes for `find` patterns.
- **Phase 4** (`mazarin/vm/`): VM interpreter adapted for flat representation —
  ~60 builtins (rectangle, trig, composite, service discovery), AttrResolver
  interface, dynamic read set tracking, DUP instruction, FlatValue ↔ vm.Value
  boundary conversion.

The frozen reference implementation lives in `mazarin/attr/inmem/` (DO NOT MODIFY).
It has generics-based `Attribute[T]` with `NewValue`, `NewConstraint`, `Get`, `Set`,
and `SetEager`. 27 tests passing. This is the API model for Phase 5.

## What to build — Phase 5: Client-Side Library (Basic)

### Read these files FIRST

Core infrastructure (read all of these before writing any code):

- `design/constraint-vm-strategic-plan.md` — Phase 5 section (lines 131-152)
- `design/constraint-vm-namespace-and-interactors.md` — URI namespace, find/deref,
  dynamic deps, damage rects, interactor framework, draw protocol
- `mazarin/attr/inmem/attribute.go` — frozen reference: `Attribute[T]`, `NewValue`,
  `NewConstraint`, `Get`, `Set`, `SetEager` (the API to emulate)
- `mazarin/attr/inmem/attr.go` — frozen reference: dirty walk, eager list, cycle detection
- `mazarin/vm/value.go` — vm.Value type with all 21 type constructors/accessors
- `mazarin/vm/resolve.go` — AttrResolver interface (Find, Deref, Exists)
- `mazarin/vm/readset.go` — ReadSet tracking (MaxReadSetSize=256)
- `mazarin/vm/flat/value.go` — FlatValue (32-byte pointer-free tagged value)
- `mazarin/vm/flat/node.go` — FlatAttrNode (128-byte attribute record)
- `mazarin/vm/flat/convert.go` — FlatValue ↔ vm.Value bridge functions
- `mazarin/vm/flat/types.go` — flat type tag constants
- `mazarin/vm/flat/composite.go` — Rectangle, Timespec, Point2D, etc. constructors

Kernel syscall interface:

- `kmazarin/ksyscall/mazzy.go` — syscall numbers and dispatch table
  - `SysAttrCreate` = 0x1021 (create attribute with URI, kind, type, optional bytecode)
  - `SysAttrWrite` = 0x1022 (write value by slot index)
  - `SysAttrWriteURI` = 0x1023 (write value by URI string)
  - `SysAttrAddDep` = 0x1024 (add single dependency edge)
  - `SysAttrUpdateDeps` = 0x1025 (replace full dependency set from read set)
  - `SysAttrRegisterQuery` = 0x1026 (register find pattern, get query result slot)
- `kmazarin/ksyscall/constraint_syscall.go` — handler implementations
- `kmazarin/ksyscall/constraint_trie.go` — namespace trie (trieInsert, trieLookup,
  trieMatchPattern)
- `kmazarin/ksyscall/constraint_dirty.go` — dirty propagation via reverse edges
- `kmazarin/ksyscall/constraint_mgr.go` — attribute slot manager

Also read:

- `shared/constants/boot_config.go` — BootConfig struct (gc_percentage, gc_percent_kernel, etc.)
- `config/kmazarin-arm64.toml` — example TOML config format
- `CLAUDE.md` — build/run instructions and mandatory rules

## Deliverables

Build the Go-friendly client library in `mazarin/attr/` (new files in existing
package, NOT in inmem). The API should feel like `mazarin/attr/inmem/` but backed
by shared pages and kernel syscalls instead of Go heap pointers.

### 1. Handle type

The central type. Wraps a kernel-managed attribute slot.

```go
// Handle[T] wraps a kernel attribute slot with type-safe access.
// T is the Go type corresponding to the attribute's FlatValue type.
type Handle[T any] struct {
    slot     uint16      // kernel-assigned slot index
    uri      string      // full URI (for diagnostics and Set-by-URI)
    kind     uint8       // AttrKindValue or AttrKindConstraint
    typ      uint8       // flat type tag (TypeI64, TypeStr, etc.)
    // Pointer to the FlatAttrNode in the shared page mapping.
    // Set after SysAttrCreate returns the slot and the shared pages
    // are mapped into the priest's address space.
    nodePtr  uintptr     // unsafe pointer to shared-page FlatAttrNode
}
```

- `h.Get() T` — read the cached value from the shared page FlatAttrNode.
  If the dirty flag is set, run the constraint's bytecode via the VM interpreter
  first (using `vm.RunWithResolver`), write back the result via `SysAttrWrite`,
  then read the updated value. For value attributes, just reads CachedValue.
- `h.Set(v T)` — write via `SysAttrWrite` syscall. **Panics on constraint
  attributes** with clear error message (same behavior as inmem).
- `h.SetEager(bool)` — set/clear the eager-notify flag in FlatAttrNode.Flags.
- `h.URI() string` — return the full URI.
- `h.IsDirty() bool` — read FlagDirty from shared page (seqlock-protected).

### 2. Typed value constructors

Create value attributes via `SysAttrCreate` syscall, return `Handle[T]`:

```go
func ValueI64(uri string, initial int64) *Handle[int64]
func ValueF64(uri string, initial float64) *Handle[float64]
func ValueBool(uri string, initial bool) *Handle[bool]
func ValueStr(uri string, initial string) *Handle[string]
func ValueTribool(uri string, initial int64) *Handle[int64]
func ValueRectangle(uri string, initial vm.Value) *Handle[vm.Value]
func ValuePoint2D(uri string, initial vm.Value) *Handle[vm.Value]
func ValueTimespec(uri string, initial vm.Value) *Handle[vm.Value]
// ... etc. for all composite types
```

Each constructor:
1. Issues `SysAttrCreate(uri, flat.AttrKindValue, typeTag, 0, 0)` syscall
2. Receives the slot index back
3. Writes the initial value via `SysAttrWrite(slot, flatValue)`
4. Returns a `Handle[T]` pointing at the shared-page FlatAttrNode

### 3. Typed constraint constructors

Create constraint attributes from pre-compiled bytecode:

```go
func ConstraintI64(uri string, prog *vm.Program, deps ...*Handle[any]) *Handle[int64]
func ConstraintF64(uri string, prog *vm.Program, deps ...*Handle[any]) *Handle[float64]
func ConstraintBool(uri string, prog *vm.Program, deps ...*Handle[any]) *Handle[bool]
func ConstraintStr(uri string, prog *vm.Program, deps ...*Handle[any]) *Handle[string]
// ... etc.
```

Each constructor:
1. Serializes bytecode into flat page representation
2. Issues `SysAttrCreate(uri, flat.AttrKindConstraint, typeTag, bytecodeOffset, bytecodeLen)`
3. For each dep handle, issues `SysAttrAddDep(newSlot, depSlot)` to register
   static dependencies
4. Returns a `Handle[T]` (dirty=true, first Get will evaluate)

### 4. Service discovery API

```go
// Find registers a URI pattern with the kernel and returns a handle to
// the kernel-maintained query result collection.
func Find(pattern string) *Handle[[]string]

// Exists registers a prefix watch and returns a handle to a kernel-maintained
// bool attribute that tracks whether any attributes exist under the prefix.
func Exists(prefix string) *Handle[bool]
```

- `Find` calls `SysAttrRegisterQuery(pattern)` → gets query result slot → wraps
  in Handle. The kernel updates the collection when namespace changes.
- `Exists` similarly registers a prefix watch.

### 5. String marshaling

Go string ↔ FlatStrRef conversion using the string data region in shared pages.
The `flat.PageRegion` already has `WriteString` and `ReadString`. The client
library needs a `PageRegion` instance backed by the priest's shared page mapping.

### 6. Pre-compiled bytecode loading

```go
// MustGetProgram loads pre-compiled bytecode from the ".constraint" ELF section.
// Panics if the program name is not found.
func MustGetProgram(name string) *vm.Program
```

This is the preferred production path. The `.constraint` ELF section contains
named bytecode programs compiled at build time. `MustGetProgram` reads from
the section, deserializes, and returns a `*vm.Program`.

For development/testing, inline compilation via `vm/compile` is still available.

## Architecture: shared page access

The client library reads FlatAttrNode values directly from shared pages (mapped
into the priest's address space). This is the "VDSO" read path — no syscall for
reads. Writes always go through syscalls because the kernel must update reverse
edges and propagate dirty flags.

**Seqlock protocol for reads:**
```go
func (h *Handle[T]) seqlockRead() FlatValue {
    for {
        seq1 := atomic.LoadUint32(&h.node().SeqCounter)
        if seq1&1 != 0 { continue } // write in progress, spin
        val := h.node().CachedValue  // read value
        seq2 := atomic.LoadUint32(&h.node().SeqCounter)
        if seq1 == seq2 { return val } // consistent read
    }
}
```

**Shared page mapping:** The priest gets shared pages mapped via `SysMapSharedPage`
(0x1011) during early init. The `attr` package needs an `Init(sharedPageBase uintptr)`
call to set up the PageRegion for reading FlatAttrNodes, strings, and collections.

## Interaction with the VM interpreter

When `Handle.Get()` encounters a dirty constraint:
1. Read the constraint's bytecode from the shared page (ProgramOffset/ProgramLen)
2. Deserialize into `*vm.Program`
3. Create an `AttrResolver` backed by shared-page reads
4. Call `vm.RunWithResolver(prog, resolver, args...)`
5. Convert the result `vm.Value` back to `FlatValue` via `flat.ValueToFlat`
6. Write back via `SysAttrWrite(slot, flatValue)` — kernel clears dirty flag
7. Compare the read set with stored deps; if changed, call `SysAttrUpdateDeps`
8. Return the typed Go value

The interpreter runs entirely in userspace (the priest). The kernel only handles
writes, dirty propagation, and dependency edge management.

## Important constraints

- **DO NOT modify `mazarin/attr/inmem/`** — frozen reference implementation.
- **Existing VM tests must continue to pass** (`mazarin/vm/*_test.go`).
- **Boundary conversion**: FlatValue on shared pages, vm.Value inside the
  interpreter. Use `flat.FlatToValue()` and `flat.ValueToFlat()`.
- **No polling or timeouts** — these are architectural changes requiring discussion.
- **Never disable async preemption or GC** — see CLAUDE.md mandatory rules.
- **Write a tactical plan first** (`design/constraint-vm-phase5-client-lib.md`)
  before implementing. Get alignment on the API design. The user wants to discuss
  architectural decisions before code is written.

## GC configuration notes

- `gc_percentage = 10000` in TOML → GOGC=10000 for priests (GOMEMLIMIT governs)
- `gc_percent_kernel = 10000` in TOML → GOGC=10000 for kernel via diplomat
- `go_mem_limit = 24` → GOMEMLIMIT=24MiB for priests
- `kernel_mem_limit = 24` → GOMEMLIMIT=24MiB for kernel
- Kernel GOMEMLIMIT and GOGC are set in `diplomat/main/startup_env.go` (envp)
- Priest GOGC is set in `kmazarin/ksyscall/launch.go`

## What comes next (Phase 6)

Phase 6 is "Dirty Notification" — eager-notify mechanism bridging kernel dirty
propagation to Go channels:
- `attr.OnDirty(attrs...) → chan struct{}`
- Kernel enqueues coalesced soft IRQ during dirty walk
- Change-gated propagation: suppress dirty if result unchanged

Phase 5 should design the Handle type with Phase 6 in mind — the eager flag
(FlagEagerNotify in FlatAttrNode.Flags) is already in the data structures,
Phase 5 just needs `SetEager(bool)` to set/clear it.

## Open design questions for the tactical plan

1. **Handle generics vs interface**: The frozen reference uses `Attribute[T]` with
   full generics. Can the client `Handle[T]` use the same pattern, or should
   composite types (Rectangle, Point2D) use `Handle[vm.Value]`? The inmem version
   can express `Attribute[int64]` directly, but shared-page values are always
   FlatValue → need type assertion at the boundary.

2. **Lazy vs eager evaluation in Get()**: Should dirty constraints always evaluate
   on `Get()` (matching inmem behavior), or should there be an option for
   batch evaluation (evaluate all dirty constraints in topological order)?

3. **Thread safety**: The inmem reference is single-threaded (global walkGen,
   evalStack). The shared-page version has natural concurrency via seqlocks.
   Does the client library need its own locking, or is the kernel's seqlock
   sufficient?

4. **PageRegion initialization**: How does the client library discover the shared
   page mapping? Via a well-known virtual address? Via auxv? Via a one-time
   init syscall? This affects the `Init()` function signature.

5. **Bytecode caching**: Should `Handle.Get()` cache the deserialized `*vm.Program`,
   or re-read from shared pages each time? The bytecode doesn't change after
   creation, so caching is safe and avoids repeated deserialization.
