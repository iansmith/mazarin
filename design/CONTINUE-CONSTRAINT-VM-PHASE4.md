# Continuation: Constraint VM Phase 4 — VM Interpreter on Flat Representation

## Context

Branch: `feature/constraint-vm`
Strategic plan: `design/constraint-vm-strategic-plan.md` (Phase 4)
Phase 1 tactical plan: `design/constraint-vm-phase1-flat-repr.md`
Phase 3 tactical plan: `design/constraint-vm-phase3-kernel-attr.md`

## What's done

- **Phases 1-3 complete.** Flat data representation (`mazarin/vm/flat/`), shared
  page allocation (`kmazarin/kmem/constraint.go`), kernel attribute management
  with 6 syscalls 0x1021-0x1026 (`kmazarin/ksyscall/constraint_*.go`), namespace
  trie, dirty propagation.
- **Original attr library preserved** as `mazarin/attr/inmem/` (27 tests passing)
  for comparison testing against the new implementation.
- **Existing VM interpreter** in `mazarin/vm/`: stack machine (`vm.go`), 128-bit
  instructions (`inst.go`), builtins (`builtin.go`), static verifier
  (`verify.go`), Go→bytecode compiler (`compile/`). All operate on heap-based
  `vm.Value` — not yet aware of flat representation.

## Phase 4 goal

Adapt the VM interpreter to operate on flat shared-page data. Add new builtins
for rectangles, trig, composite types, and service discovery. Add dynamic read
set tracking.

## Key files to read first

- `mazarin/vm/vm.go` — current interpreter loop
- `mazarin/vm/value.go` — heap-based Value type
- `mazarin/vm/inst.go` — instruction encoding (128-bit, already fixed-width)
- `mazarin/vm/builtin.go` — existing builtins (min, max, abs, coll_*, str_*)
- `mazarin/vm/verify.go` — static verifier
- `mazarin/vm/flat/value.go` — FlatValue (32 bytes, 21 type tags)
- `mazarin/vm/flat/composite.go` — Rectangle, Timespec, Point, etc. constructors
- `mazarin/vm/flat/types.go` — type tag constants
- `mazarin/vm/flat/node.go` — FlatAttrNode (128 bytes)
- `kmazarin/ksyscall/constraint_trie.go` — namespace trie (trieInsert, trieLookup, trieMatchPattern)
- `kmazarin/ksyscall/constraint_syscall.go` — SysAttrUpdateDeps (dynamic read set)
- `design/constraint-vm-namespace-and-interactors.md` — Part 2 has dynamic dep tracking design

## What to build (write a tactical plan first, then implement)

1. **FlatValue ↔ vm.Value bridge in the interpreter**: The interpreter currently
   pushes/pops `vm.Value`. Phase 4 needs it to read attribute slot values as
   `FlatValue` from shared pages and convert to `vm.Value` for stack operations
   (or operate on `FlatValue` directly — design decision).

2. **New builtins** (add to `builtin.go` and register in the builtin table):
   - Rectangle: `rect`, `rect_union`, `rect_intersect`, `rect_overlaps`,
     `rect_contains`, `rect_empty`, `rect_area`, `rect_width`, `rect_height`
   - Trig: `sin`, `cos`, `tan`, `asin`, `acos`, `atan2`, `deg_to_rad`,
     `rad_to_deg`, `absf`, `pow`
   - Composite constructors/accessors: `timespec`, `timespec_seconds`,
     `timespec_nanos`, `timezone`, `tz_convert`, `duration`, `duration_nanos`,
     `date`, `date_year`, `date_month`, `date_day`, `point2d`, `point2d_x`,
     `point2d_y`, `point3d`, `pointf2d`, `pointf3d`, `ipv4`, `ipv4_octet`,
     `ipv6`, `priest_id`, `priest_id_num`, `maz_id`, `maz_id_num`
   - Service discovery: `find`, `deref_i64`, `deref_str`, `deref_bool`,
     `deref_f64`, `deref_rect`, `deref_point2d`, `deref_tribool`, `exists`,
     `uri_segment`, `is_unknown`

3. **Dynamic read set tracking**: During evaluation, record all attribute slots
   accessed via find/deref/exists. After evaluation, compare with stored deps.
   If changed, call SysAttrUpdateDeps. Design in
   `constraint-vm-namespace-and-interactors.md` Part 2 (Approach A).

4. **Verifier updates**: `deref_*` returns typed-or-unknown. `find` returns
   `collection<str>`. Fuel limit must bound trie walks.

5. **Tests**: Unit tests for all new builtins. Comparison tests against
   `mazarin/attr/inmem/` for matching behavior on shared test cases.

## Important constraints

- DO NOT modify `mazarin/attr/inmem/` — it's the frozen reference copy.
- The existing heap-based VM tests must continue to pass.
- Read `CLAUDE.md` for build/run instructions and mandatory rules (never disable
  async preemption or GC).
- Start by writing a tactical plan document (`design/constraint-vm-phase4-vm-flat.md`)
  before implementing. Get alignment on the design decision: does the interpreter
  operate on FlatValue directly, or convert to vm.Value at the boundary?
