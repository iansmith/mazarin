# Constraint VM: Strategic Implementation Plan

Ian Smith, March 2026

See `constraint-vm-vdso-architecture.md` for the core shared-page/VDSO design.
See `constraint-vm-namespace-and-interactors.md` for URI namespace, service
discovery, and the interactor/damage framework.

The constraint implementation is the UI of mazarin. Everything — layout,
damage tracking, service discovery, input routing, cross-priest communication
— flows through the attribute graph. This plan reflects that.

## Status Key

- **pending** — not started
- **planning** — tactical plan created, not yet implementing
- **in-progress** — actively implementing
- **complete** — implemented and tested

---

## Phase 1: Flat Data Representation — planning

Design and implement the pointer-free, fixed-size attribute node format for the
shared pages. This is foundational — every subsequent phase builds on it.

- FlatValue (32 bytes): supports all value types — I64, F64, Bool, Tribool,
  Str, Timespec, Timezone, Duration, Date, Point2D, Point3D, PointF2D, PointF3D,
  Radians, Degrees, IPv4, IPv6, PriestId, MazId, **Rectangle (int32 coords)**,
  and Collection (element-typed)
- **Rectangle type** (`TypeRectangle = 0x15`): `{X0, Y0, X1, Y1 int32}` — 16
  bytes, fits in FlatValue.Data. Used for damage rectangles, clipping, bounds.
  int32 coordinates are sufficient for pixel-space UI.
- Composite types: PriestId (id + name string ref), MazId (id + path string ref),
  Timespec (seconds + nanos), Date (year + month + day + timezone), Points (2-3
  components, int64 or float64), Rectangle (4 int32 components)
- Flat attribute node struct (owner, type, dirty flag, seqlock counter, value,
  dependency edges as slot indices)
- Flat string representation (fixed-size NUL-terminated buffer, 256-byte slots)
- Flat collection representation (offset + count + element type into value array
  region); collections of all scalar types supported
- Page layout: header, attribute slot array, bytecode region, string/data region
- Slot allocator (bitmap or free-list over the fixed-size slot array)
- **DUP instruction** (`OpDup = 0x12`): duplicate top of stack. Needed for
  patterns where a value must be both inspected and returned.

Dependencies: none.
Tactical plan: `constraint-vm-phase1-flat-repr.md`

## Phase 2: Shared Page Allocation and Mapping — pending

Kernel allocates the shared pages and maps them into priest address spaces at a
fixed virtual address.

- Kernel-side page allocation (from the existing frame pool)
- Map into every priest's address space during priest launch
- Fixed VA selection (similar to .mzr slot addresses)
- Kernel-side functions for creating/destroying attribute slots
- **Namespace trie region**: flat, pointer-free trie over URI segments. TrieNode
  (128 bytes): segment string, firstChild, nextSibling, attrSlot, childCount,
  seqlock. Initial capacity: 2048 nodes. Mapped read-only to priests, writable
  by kernel. See `constraint-vm-namespace-and-interactors.md` Part 1.
- **Per-priest scratch pages**: one writable page per priest for resolution
  caches and read sets during evaluation. Mapped writable only to the owning
  priest.
- **Generation counter** in page header: bumped on attribute destruction,
  used by VDSO to invalidate resolution caches.

Dependencies: Phase 1.

## Phase 3: Kernel Attribute Management — pending

The kernel can create, write, and dirty-propagate attributes in the shared
pages. The kernel is the authority on the namespace.

- Kernel-side Set(): write value + seqlock + dirty propagation walk
- **Set() enforcement**: panic if called on a constraint attribute. Value
  attributes only. This is a fundamental constraint system invariant.
- Dirty propagation: DFS over dependency edges, generation counter for diamonds
- New syscalls: SysAttrWrite, SysAttrCreate, SysAttrAddDep
- Ownership tracking: each slot stores owning priest ID
- **URI registration**: SysAttrCreate takes a URI string. Kernel inserts into
  the namespace trie. URI structure: `attr:///priest/<name>/<type>/<path...>`
  or `attr:///kernel/<type>/<path...>`. Type segment provides naming-level type
  safety.
- **Query pattern management**: SysAttrRegisterQuery(pattern) creates a
  kernel-maintained query result attribute (collection type). Kernel checks
  registered patterns on every SysAttrCreate/destroy and updates matching query
  result collections, triggering dirty propagation. Patterns support `*`
  wildcards over URI segments.
- **SysAttrUpdateDeps(slot, readSet, count)**: update dependency edges after
  evaluation when the dynamic read set changed. Atomically updates forward
  edges (deps) and reverse edges (dependents).

Dependencies: Phase 2.

## Phase 4: VM Interpreter on Flat Representation — pending

Modify the constraint VM interpreter to operate on the flat shared-page data
format instead of Go heap objects. Add namespace and discovery builtins.

- Interpreter reads/writes values from/to shared page attribute slots
- String operations work with fixed-size NUL-terminated buffers
- Collection operations work with offset/count/element-type representation
- All value types supported including **Rectangle**
- New builtins: trig functions (sin, cos, tan, asin, acos, atan2), absf, pow,
  constructors/accessors for composite types, timezone conversion
- **Rectangle builtins**: `rect`, `rect_union`, `rect_intersect`,
  `rect_overlaps`, `rect_contains`, `rect_empty`, `rect_area`, `rect_width`,
  `rect_height`. EMPTY_RECT sentinel: `{0,0,0,0}`.
- **Service discovery builtins**:
  - `find(pattern) → collection of str` — pattern match against namespace
  - `deref_<type>(uri) → value or unknown` — resolve URI, read value
    (typed variants: deref_i64, deref_str, deref_bool, deref_point2d,
    deref_rect, deref_f64, deref_tribool, etc.)
  - `exists(prefix) → bool` — true if any children under URI prefix
  - `uri_segment(uri, index) → str` — extract segment from URI
  - `is_unknown(value) → bool` — check for unknown/tombstone sentinel
- **Dynamic read set tracking**: during evaluation, the VM records all
  attribute slots accessed via find/deref/exists into a read set (stored in
  the per-priest scratch page). After evaluation, the read set is compared
  with stored dependency edges. If changed, SysAttrUpdateDeps is called.
  If unchanged (steady state), no syscall. See Approach A in
  `constraint-vm-namespace-and-interactors.md` Part 2.
- Verifier updates: `deref` returns typed-or-unknown; verifier ensures unknown
  is handled. `find` returns collection-of-str. Fuel limit bounds all
  operations including trie walks.

Dependencies: Phase 1. Can proceed in parallel with Phases 2-3.

## Phase 5: Client-Side Library (Basic) — pending

Go-friendly wrapper hiding shared page access, type marshaling, and syscalls.

- Typed value constructors for all types (attr.ValueI64, attr.ValueTimespec,
  attr.ValueRectangle, etc.) — create via syscall with URI
- Typed constraint constructors (attr.NewConstraintI64, etc.) — compile +
  create via syscall
- handle.Get() — direct shared page read, run interpreter if dirty
- handle.Set(v) — write via SysAttrWrite syscall. **Panics on constraint
  attributes** with clear error message.
- String marshaling: Go string ↔ fixed-size buffer
- **attr.Find(pattern)** — register query pattern, return handle to
  kernel-maintained collection attribute
- **attr.Exists(prefix)** — register prefix watch, return handle to
  kernel-maintained bool attribute
- **MustGetProgram(name)** — load pre-compiled bytecode from `.constraint`
  ELF section. Panics if not found. This is the preferred production path;
  inline Go compilation (attr.NewConstraintStr with Go source) is kept for
  examples and development.

Dependencies: Phases 3 and 4.

## Phase 6: Dirty Notification — pending

Eager-notify mechanism bridging kernel dirty propagation to Go channels.

- Eager-notify flag on attribute slots
- Kernel enqueues coalesced soft IRQ during dirty walk
- Client library: attr.OnDirty(attrs...) → chan struct{}
- Change-gated propagation: suppress dirty if result unchanged

Dependencies: Phase 5.

## Phase 7: Kernel-Published Attributes — pending

Kernel publishes system state as attributes. First end-to-end demo.

- `attr:///kernel/int64/time/utc_seconds` — updated once per second
- `attr:///kernel/int64/mouse/x`, `attr:///kernel/int64/mouse/y` — device IRQs
- `attr:///kernel/bool/mouse/leftDown` — button state
- `attr:///kernel/int64/screen/width`, `attr:///kernel/int64/screen/height`
- `attr:///kernel/bool/darkMode` — system-wide theme toggle
- Device interrupt bottom-halves call Set() on kernel-owned attributes

**Milestone: clock app demo** — 7 timezone constraints, OnDirty, redraw.
Demonstrates full end-to-end: kernel time → constraint evaluation → display.

Dependencies: Phase 6.

---

Phases 1-7 deliver the complete constraint programming model with namespace
and service discovery. Phases 8+ build the UI framework, add hardening,
optimization, and interactivity.

---

## Phase 8: Interactor Framework — pending

Standard interactor attributes, the damage rectangle model, and the draw
protocol. This is where the constraint system becomes a UI toolkit.

See `constraint-vm-namespace-and-interactors.md` Parts 3-4 for full details.

- **Standard interactor attributes**: every interactor publishes originPoint,
  width, height, upperLeft, lowerRight, bounds (Rectangle), parent, visible,
  content, bgColor, textColor, damageRect, and corresponding lastPainted*
  value attributes.
- **Interactor library** (`mazarin/interactor`): NewWindow, NewCard, NewLabel,
  NewRow, NewColumn, NewDeck — each publishes standard attributes with
  appropriate default constraints. App authors compose interactors; the library
  handles attribute publishing, damage computation, and draw protocol.
- **damageRect constraint**: replaces boolean drawingDirty. Each interactor's
  damageRect compares current visual attributes against lastPainted* values.
  Non-empty means "I need repainting, here's the affected region." Includes
  rect_union of current and lastPainted bounds to handle exposed regions when
  interactors shrink or become invisible.
- **Damage propagation policies**: each parent type has its own damageRect
  constraint encoding its damage policy:
  - *Naive*: any child damaged → mark whole parent damaged. Simple, often
    fast enough.
  - *Precise*: tight bounding box of damaged children's regions.
  - *Deck of cards*: ignore non-selected children's damage entirely.
  - *Row with overlap*: expand damage to overlapping neighbors.
- **Draw protocol**: three-phase (pre-draw, recurse children, post-draw).
  The damage rect IS the clipping rect — dapope or the parent sets clipping
  to the computed damageRect, then draw freely. Anything outside the clip is
  harmless. Parent decides child z-order and visibility in the draw walk.
  Priest owns its interactor tree and draw recursion.
- **Layout patterns**:
  - *Outside-in*: window has fixed size, children constrain to parent.
    `width = min(contentWidth, parent.innerWidth)`.
  - *Inside-out*: children have natural size, parents expand.
    `window.width = clamp(card.width + margin, minWidth, maxWidth)`.
  - *Visible as universal show/hide*: deck selection, checkbox toggles,
    minimum-size cutoffs, row overflow hiding. `visible` is always a
    constraint (not Set-able).
- **After painting**: dapope updates lastPainted* values → damageRect
  re-evaluates to EMPTY_RECT → damage clears up the tree via change-gated
  propagation.

**Milestone: static UI demo** — window containing a card with a centered
label. No interactivity. Label content from a constraint. Damage tracking
works: change label text → only the affected region repaints.

Dependencies: Phase 7.

## Phase 9: Input Routing (Dapope Layer) — pending

Three-layer input model: kernel → dapope → priest.

- Dapope hit-testing against interactor bounds (reads bounds attributes via
  VDSO). Z-ordered window list.
- `attr:///priest/dapope/int64/focus/priestId` and
  `attr:///priest/dapope/bool/focus/leftDown` — routed input attributes
- Ordering: set priestId before leftDown on press, clear leftDown before
  priestId on release
- Window bounds as attributes — dapope reads them for hit-testing
- Application priests consume routed input through pure constraints:
  `myPressed = constraint: focus.leftDown && focus.priestId == myId`
- Drag semantics: dapope latches focus.priestId on press, doesn't
  re-hit-test until release

**Milestone: button demo** — hover highlight, press state, toggle 12/24 hour
format on the clock app. Interactive UI entirely from constraints.

**Milestone: cross-priest discovery demo** — transcript app discovers
calendar priest's event title via find + deref. No lifecycle management code.
Calendar start/stop handled automatically by namespace dirty propagation.

Dependencies: Phase 8.

## Phase 10: Synchronization (Multi-Core) — pending

- vdsoCritical counter, timer IRQ check, SIGURG check
- Per-attribute seqlocks with acquire/release (STLR/LDAR on ARM64, fences on
  RISC-V, TSO on x86_64)
- **Namespace trie seqlocks**: VDSO trie walk uses seqlock-read protocol,
  retries on concurrent kernel mutation
- Stress test with concurrent readers/writers across priests

Dependencies: Phase 5. Can develop in parallel with 8-9, test after.

## Phase 11: VDSO Injection — pending

Package VM interpreter as VDSO code injected into priest address spaces.

- VDSO format determination and injection during priest launch
- Get() calls into VDSO instead of inline interpreter
- **Namespace trie walking in VDSO**: deref resolves URIs by walking the
  trie in shared pages — no syscall for URI resolution
- **Resolution cache management**: VDSO maintains per-constraint resolution
  caches in the scratch page. Cache invalidation via generation counter.
- Performance optimization: zero-syscall reads in steady state

Dependencies: Phase 10.

## Phase 12: .constraint ELF Sections — pending

Move constraint compilation from runtime to build time.

- Build toolchain emits .constraint ELF section with pre-compiled bytecode
- Kernel extracts, re-verifies, copies to shared pages
- **MustGetProgram(name) API**: load named program from ELF section at runtime.
  Panics if not found. Production path for all constraint programs.
- Inline Go compilation (attr.NewConstraintStr with Go source) retained for
  examples, development, and REPL-style experimentation
- Deployment optimization: priest doesn't need go/parser + go/types

Dependencies: Phase 5. Can develop in parallel with 8-11.

## Phase 13: Priest Death and Cleanup — pending

Tombstone propagation, namespace cleanup, and ownership-based teardown.

- Kernel walks shared pages during priest teardown
- No dependents: free slot. Has dependents: tombstone with "unknown" sentinel
- Reset vdsoCritical, fix odd seqlock counters
- **Namespace cleanup**: remove dead priest's URIs from the trie, decrement
  ChildCount on ancestor nodes. Update registered query patterns — remove
  dead URIs from query result collections, dirty-propagate.
- **Resolution cache invalidation**: bump generation counter so all priests'
  VDSO caches flush stale entries pointing to tombstoned slots.
- **Interactor cleanup**: dead priest's interactors' damageRect attributes
  propagate tombstone/unknown. Parent interactors in other priests see the
  change via find/deref → their damageRect constraints re-evaluate → the
  dead priest's screen region is repainted by the parent.

Dependencies: Phase 3. Test benefits from full stack (8-9).

## Milestones Summary

| Milestone | After Phase | Demonstrates |
|---|---|---|
| Shared pages working | 2 | Kernel and priest share memory at fixed VA, namespace trie populated |
| Constraints evaluate on shared data | 4 | VM runs on flat repr with find/deref/Rectangle |
| Programming model usable | 5 | Priests create constraints via API, discover services via attr.Find() |
| **Clock app demo** | **7** | **Kernel time → constraints → display. Full programming model.** |
| **Static UI demo** | **8** | **Interactor tree, damage rects, draw protocol. Change label → minimal repaint.** |
| **Interactive UI** | **9** | **Button, mouse routing, toggle. Cross-priest discovery (transcript↔calendar).** |
| Multi-core safe | 10 | Concurrent priests, seqlocks, no races |
| Zero-syscall reads | 11 | VDSO-injected interpreter with trie walking |
| Build-time constraints | 12 | .constraint ELF sections, MustGetProgram |
| Fault-tolerant | 13 | Priest death → tombstone → namespace cleanup → UI recovery |

## Dependency Graph

```
Phase 1 (flat data + Rectangle + DUP)
  ├─→ Phase 2 (shared pages + trie + scratch pages)
  │     └─→ Phase 3 (kernel mgmt + URI + queries + Set rule)
  │           ├─→ Phase 5 (client lib + Find + Exists + MustGetProgram)
  │           │     ├─→ Phase 6 (dirty notification)
  │           │     │     └─→ Phase 7 (kernel attrs) ── CLOCK DEMO
  │           │     │           └─→ Phase 8 (interactors + damage) ── STATIC UI DEMO
  │           │     │                 └─→ Phase 9 (input routing) ── INTERACTIVE DEMO
  │           │     ├─→ Phase 10 (multi-core sync)
  │           │     │     └─→ Phase 11 (VDSO injection)
  │           │     └─→ Phase 12 (.constraint ELF)
  │           └─→ Phase 13 (priest death + namespace cleanup)
  └─→ Phase 4 (VM + find/deref/rect builtins + dynamic deps)
        └─→ (merges into Phase 5)
```

Phases 4 and 2-3 can proceed in parallel. Phase 10-12 can proceed in parallel
with 8-9 (multi-core sync and VDSO don't block the interactor framework).
Phase 12 (.constraint ELF) can start anytime after Phase 5.
