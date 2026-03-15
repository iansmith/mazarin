# Phase 1: Flat Data Representation — Tactical Plan

Strategic context: `constraint-vm-strategic-plan.md` (Phase 1)
Architecture: `constraint-vm-vdso-architecture.md`
Namespace and interactors: `constraint-vm-namespace-and-interactors.md`

## Goal

Design and implement pointer-free, fixed-size data structures that can live in
shared memory pages outside the Go heap. This is the foundational data layer
that all subsequent phases build on.

## Current State

The current implementation uses Go heap objects throughout:

- `vm.Value` has `str string` and `coll []Value` fields (heap pointers)
- `attr.Attribute[T]` uses `func() T` closures and `[]*node` slices
- `vm.Program` uses `[]Inst`, `[]string`, `[]FuncInfo` slices
- Dependency graph uses pointer-based adjacency lists

Key files:
- `mazarin/vm/value.go` — Value type (lines 27-33)
- `mazarin/vm/inst.go` — Inst struct (128-bit, already fixed-width)
- `mazarin/vm/vm.go` — Program struct (lines 19-36), machine struct (lines 81-92)
- `mazarin/attr/attribute.go` — Attribute[T], NewValue, NewConstraint
- `mazarin/attr/attr.go` — node struct, dirty propagation, cycle detection

## What We're Building

A parallel set of "flat" types that can represent the same information in a
contiguous byte region without Go pointers. The existing heap-based types remain
for now (they're used by the compiler and tests). The flat types will be used by
the shared-page runtime in later phases.

## Design Decisions

### 1. Flat Value Representation

Replace the heap-containing `Value` struct with a fixed-size representation.

```go
// FlatValue is 32 bytes, no pointers.
// Strings and collections are references into other regions of the shared pages.
type FlatValue struct {
    Typ    uint8     // one of the 21 type tags below
    _pad   [7]byte   // alignment
    Data   [24]byte  // interpreted based on Typ
}
```

The 24-byte Data field accommodates the largest inline types (Point3D and
PointFloat3D: 3 × 8 bytes).

**Type tags (21 tags for 24 logical types):**

```go
const (
    TypeI64       = 0x01
    TypeF64       = 0x02
    TypeBool      = 0x03
    TypeTribool   = 0x04
    TypeStr       = 0x05
    TypeTimespec  = 0x06  // {Seconds int64, Nanos int32} — always UTC
    TypeTimezone  = 0x07  // {OffsetMinutes int16}
    TypeDuration  = 0x08  // {Nanos int64}
    TypeDate      = 0x09  // {Year int16, Month uint8, Day uint8, TzMinutes int16}
    TypePoint2D   = 0x0A  // {X int64, Y int64}
    TypePoint3D   = 0x0B  // {X int64, Y int64, Z int64}
    TypePointF2D  = 0x0C  // {X float64, Y float64}
    TypePointF3D  = 0x0D  // {X float64, Y float64, Z float64}
    TypeRadians   = 0x0E  // {Val float64}
    TypeDegrees   = 0x0F  // {Val float64}
    TypeIPv4      = 0x10  // {Addr [4]byte}
    TypeIPv6      = 0x11  // {Addr [16]byte}
    TypePriestId  = 0x12  // {Id uint16, NameOffset uint32, NameLen uint16}
    TypeMazId     = 0x13  // {Id uint16, PathOffset uint32, PathLen uint16}
    TypeCollection = 0x14 // FlatCollRef: {ElemType uint8, RegionOffset uint32, Count uint16}
    TypeRectangle  = 0x15 // {X0 int32, Y0 int32, X1 int32, Y1 int32} — damage rects, clipping, bounds
)
```

**Data field interpretation by type:**

| Type | Data layout | Size used |
|---|---|---|
| I64 | int64 at offset 0 | 8 bytes |
| F64 | float64 at offset 0 | 8 bytes |
| Bool | uint8 at offset 0 (0 or 1) | 1 byte |
| Tribool | uint8 at offset 0 (0, 1, or 2) | 1 byte |
| Str | FlatStrRef: {RegionOffset uint32, Len uint16} | 6 bytes |
| Timespec | {Seconds int64 at 0, Nanos int32 at 8} | 12 bytes |
| Timezone | {OffsetMinutes int16 at 0} | 2 bytes |
| Duration | {Nanos int64 at 0} | 8 bytes |
| Date | {Year int16 at 0, Month uint8 at 2, Day uint8 at 3, TzMinutes int16 at 4} | 6 bytes |
| Point2D | {X int64 at 0, Y int64 at 8} | 16 bytes |
| Point3D | {X int64 at 0, Y int64 at 8, Z int64 at 16} | 24 bytes |
| PointF2D | {X float64 at 0, Y float64 at 8} | 16 bytes |
| PointF3D | {X float64 at 0, Y float64 at 8, Z float64 at 16} | 24 bytes |
| Radians | {Val float64 at 0} | 8 bytes |
| Degrees | {Val float64 at 0} | 8 bytes |
| IPv4 | {Addr [4]byte at 0} | 4 bytes |
| IPv6 | {Addr [16]byte at 0} | 16 bytes |
| PriestId | {Id uint16 at 0, NameOffset uint32 at 4, NameLen uint16 at 8} | 10 bytes |
| MazId | {Id uint16 at 0, PathOffset uint32 at 4, PathLen uint16 at 8} | 10 bytes |
| Collection | FlatCollRef: {ElemType uint8 at 0, RegionOffset uint32 at 4, Count uint16 at 8} | 10 bytes |
| Rectangle | {X0 int32 at 0, Y0 int32 at 4, X1 int32 at 8, Y1 int32 at 12} | 16 bytes |

**Rectangle — int32 coordinates:**

Rectangle uses int32 coordinates (not int64) to fit in the 24-byte Data field
(16 bytes for 4 × int32). Screen/pixel coordinates never exceed int32 range
(max ~2 billion pixels). This avoids expanding FlatValue beyond 32 bytes.
EMPTY_RECT sentinel: `{0, 0, 0, 0}`. `rect_empty` checks `X0 >= X1 || Y0 >= Y1`.

Rectangle is used for damage rectangles, clipping rectangles, and interactor
bounds throughout the interactor framework (see
`constraint-vm-namespace-and-interactors.md` Part 3).

Strings and collections are not inline — they reference data in a separate
region of the shared pages.

**Composite types — PriestId and MazId:**

PriestId and MazId are composites: a numeric identifier plus a string reference
(the priest's name or the maz's path). The string reference points into the
string data region, allowing O(1) access to the human-readable name without
storing it inline. The numeric id is the kernel-assigned identifier, constrained
by the maximum number of priests/maz modules in the system.

**Timespec — UTC only:**

Timespec stores UTC time as seconds + nanoseconds (12 bytes). Timezone
conversion is handled by the `tz_convert` builtin, which takes a Timespec and a
Timezone and produces a new Timespec. There is no local-time Timespec — all
times in the shared pages are UTC, and display-local conversion happens in
constraints.

### 2. Flat String Representation

Strings in the shared pages are stored in a string data region as fixed-size
slots.

```go
const FlatStringMaxLen = 255  // max string length (NUL-terminated, 256-byte slot)
const FlatStringSlotSize = 256

// In the string data region: [slot0: 256 bytes][slot1: 256 bytes]...
// Each slot is a NUL-terminated UTF-8 string, max 255 bytes of content.
```

A FlatStrRef's RegionOffset is the byte offset into the string data region
(i.e., slot index * 256). The Len field is the string length (excluding NUL),
allowing O(1) length queries without scanning for NUL.

**Decision: 256-byte slots.** Most layout strings (labels, widget names) are
under 32 bytes. 256 bytes covers any reasonable UI string. If we later need
longer strings, we can add a "long string" slot size (e.g., 1024 bytes) with a
flag bit in the type tag.

### 3. Flat Collection Representation

Collections are stored in a collection data region as contiguous arrays of
FlatValue. Each collection has a single element type — all elements must be the
same type. This is enforced by the compiler and verifier.

```go
// A FlatCollRef is stored in a TypeCollection FlatValue's Data field.
// ElemType is one of the scalar type tags (TypeI64 through TypeMazId).
// RegionOffset is the byte offset into the collection data region.
// Count is the number of elements.
// Each element is a FlatValue (32 bytes).
//
// The collection data region: [elem0: 32B][elem1: 32B][elem2: 32B]...
type FlatCollRef struct {
    ElemType     uint8   // scalar type of each element
    _pad         [3]byte // alignment
    RegionOffset uint32  // byte offset into collection data region
    Count        uint16  // number of elements
}
```

Collections of all 20 scalar types are supported (everything except Collection
itself — no nested collections). This includes collections of composite types:
`[]Timespec`, `[]Point2D`, `[]IPv6`, `[]PriestId`, `[]Rectangle`, etc.

Collections within collections are not supported (the constraint VM already
rejects nested collections), so collection elements are always scalar
FlatValues.

### 4. Flat Attribute Node

```go
// FlatAttrNode is the per-attribute record in the shared pages.
// Fixed size, no pointers.
type FlatAttrNode struct {
    // Identity and ownership
    Owner       uint16    // priest ID (0 = kernel)
    Kind        uint8     // AttrKindValue or AttrKindConstraint
    ValueType   uint8     // TypeI64, TypeF64, TypeBool, TypeStr, etc.

    // State flags
    Flags       uint32    // dirty (bit 0), eager-notify (bit 1), tombstoned (bit 2)

    // Synchronization
    SeqCounter  uint32    // seqlock: odd = write in progress, even = stable

    // Cached value (written by evaluator, read by Get())
    CachedValue FlatValue // 32 bytes

    // Constraint program (only for AttrKindConstraint)
    ProgramOffset uint32  // byte offset into bytecode region (0 = none)
    ProgramLen    uint16  // number of instructions

    // Dependencies (who I depend on — my inputs)
    DepsOffset  uint32    // byte offset into edge array region
    DepsCount   uint16    // number of dependencies

    // Dependents (who depends on me — notified when I change)
    DependentsOffset uint32  // byte offset into edge array region
    DependentsCount  uint16  // number of dependents

    // Name (for kernel-published and debugging)
    NameOffset  uint32    // byte offset into string data region (0 = unnamed)

    // Padding to cache-line alignment
    _pad        [N]byte   // pad to 128 bytes total (2 cache lines on ARM64)
}
```

**Target size: 128 bytes per attribute node** (2 cache lines on ARM64's 64-byte
lines, 2 cache lines on x86_64). This gives predictable layout and avoids false
sharing when adjacent attributes are accessed by different cores.

### 5. Edge Array

Dependency edges are stored as arrays of uint16 slot indices in a dedicated
edge array region.

```go
// Edge array region: [slot_idx: uint16][slot_idx: uint16]...
// FlatAttrNode.DepsOffset / DependentsOffset point into this region.
// FlatAttrNode.DepsCount / DependentsCount give the array length.
```

Using uint16 limits us to 65,535 attribute slots. For a UI system this is more
than sufficient (a complex desktop might have a few thousand attributes).

### 6. DUP Instruction

The existing VM instruction set needs a DUP instruction for patterns where a
value must be both inspected and returned (e.g., check `coll_len` then use the
collection for `coll_first`). Without DUP, these patterns require a
load/store round-trip through locals.

```go
OpDup uint8 = 0x12  // push stack[sp-1] again
```

Implementation in `vm.go`:

```go
case OpDup:
    if m.sp <= 0 {
        return m.haltf("DUP on empty stack")
    }
    return m.push(m.stack[m.sp-1])
```

Verifier: DUP requires sp >= 1. The pushed value has the same type as TOS.

### 7. Shared Page Layout

```
+---------------------------+ offset 0
| Page Header               |
|   magic, version          |
|   slot count, free bitmap |
|   region offsets/sizes    |
+---------------------------+ offset H
| Attribute Node Array      |
|   [node 0: 128 bytes]     |
|   [node 1: 128 bytes]     |
|   ...                     |
+---------------------------+ offset A
| Edge Array Region         |
|   [uint16 slot indices]   |
+---------------------------+ offset E
| Bytecode Region           |
|   [Inst: 16 bytes each]   |
+---------------------------+ offset B
| String Data Region        |
|   [256-byte string slots] |
+---------------------------+ offset S
| Collection Data Region    |
|   [FlatValue: 32 bytes]   |
+---------------------------+ offset C
| Namespace Trie Region     |
|   [TrieNode: 128 bytes]   |
+---------------------------+ offset T
| Free/growth space         |
+---------------------------+ end of mapped region
```

The namespace trie region is defined here for page layout purposes but is not
populated until Phase 2 (shared page allocation) and Phase 3 (kernel attribute
management). See `constraint-vm-namespace-and-interactors.md` Part 1 for the
TrieNode structure.

The page header stores offsets and capacities for each region, allowing the
regions to be sized independently. Initial sizing (will need tuning):

| Region | Initial capacity | Size |
|---|---|---|
| Attribute nodes | 512 slots | 64 KB |
| Edge array | 4096 edges | 8 KB |
| Bytecode | 2048 instructions | 32 KB |
| String data | 256 strings | 64 KB |
| Collection data | 1024 elements | 32 KB |
| Namespace trie | 2048 nodes | 256 KB |
| **Total** | | **~456 KB** (114 4KB pages) |

### 8. Slot Allocator

A simple bitmap allocator for attribute slots. One bit per slot; 512 slots =
64 bytes of bitmap (fits in the page header).

```go
// AllocSlot scans the bitmap for a free slot, marks it allocated, returns the
// slot index. Returns -1 if full.
func (h *PageHeader) AllocSlot() int16

// FreeSlot marks a slot as free.
func (h *PageHeader) FreeSlot(idx int16)
```

String slots, collection elements, edge array entries, and bytecode space use
similar bitmap or bump allocators within their respective regions.

## Package Structure

New package: `mazarin/vm/flat`

This keeps the flat representation separate from the existing heap-based types.
Later phases will wire them together.

Files:

| File | Contents |
|---|---|
| `flat/types.go` | Type tag constants (21 tags), type metadata (size, name, is-composite) |
| `flat/value.go` | FlatValue, FlatStrRef, FlatCollRef, accessor methods for all 24 types |
| `flat/composite.go` | Composite type helpers: Timespec, Date, Point*, Rectangle, PriestId, MazId constructors/accessors |
| `flat/node.go` | FlatAttrNode, flag constants, accessor methods |
| `flat/layout.go` | PageHeader, region offsets, shared page layout |
| `flat/alloc.go` | Bitmap slot allocator for nodes, strings, collections, edges |
| `flat/convert.go` | Conversion between vm.Value ↔ FlatValue (for testing and the compiler bridge) |
| `flat/flat_test.go` | Tests for all of the above |

## Conversion Functions

For bridging between the existing heap-based types and the flat representation
(needed by the compiler in Phase 5, and for testing now):

```go
// ValueToFlat converts a heap-based vm.Value to a FlatValue.
// Strings and collections are allocated in the provided page region.
func ValueToFlat(v vm.Value, page *PageRegion) FlatValue

// FlatToValue converts a FlatValue back to a heap-based vm.Value.
// Reads string/collection data from the provided page region.
func FlatToValue(fv FlatValue, page *PageRegion) vm.Value
```

## Testing Strategy

All tests are pure Go unit tests — no kernel, no shared pages, no VDSO. We
allocate a `[]byte` buffer, treat it as a simulated shared page region, and
exercise the flat types against it.

1. **FlatValue round-trip**: Create vm.Values of every type (all 24), convert
   to FlatValue, convert back, verify equality. Covers scalar types (I64, F64,
   Bool, Tribool), string references, Rectangle, and all composite types
   (Timespec, Timezone, Duration, Date, Point2D/3D, PointF2D/3D, Radians,
   Degrees, IPv4, IPv6, PriestId, MazId).
2. **String allocation**: Allocate string slots, write strings, read back,
   verify content and length. Test max-length strings. Test NUL termination.
3. **Collection allocation**: Allocate typed collections for several element
   types (I64, Str, Point2D, IPv6, PriestId), write values, read back via
   FlatCollRef, verify contents and element type tag.
4. **Composite type accessors**: Construct each composite type via helper
   functions, extract fields, verify round-trip. Test boundary values (e.g.,
   max timezone offset ±720 minutes, IPv6 all-zeros/all-ones).
5. **Attribute node creation**: Allocate slots, set fields, read back, verify
   all fields including ValueType for each of the 20 type tags.
6. **Edge array**: Allocate dependency edges, link nodes, traverse edges,
   verify connectivity.
7. **Slot allocator**: Allocate all slots, verify full. Free some, reallocate,
   verify reuse. Bitmap correctness.
8. **Page layout**: Create a PageHeader, allocate nodes/strings/collections,
   verify no region overlap, verify offsets are correct.

## What This Phase Does NOT Include

- No kernel integration (Phase 2)
- No shared memory mapping (Phase 2)
- No syscalls (Phase 3)
- No VM interpreter changes (Phase 4)
- No client library (Phase 5)
- No dirty propagation (Phase 3)
- No seqlocks or synchronization (Phase 8)
- No VDSO (Phase 9)

This phase is purely data structure design and implementation with unit tests.

## New Builtins Required (Implemented in Later Phases)

Phase 1 defines the flat representation for all types. The builtins that
operate on these types are implemented in Phase 4 (VM interpreter) but listed
here for completeness since the type system drives them:

**Trigonometry** (operate on Radians/Degrees, return F64):
- `sin(Radians) → F64`, `cos(Radians) → F64`, `tan(Radians) → F64`
- `asin(F64) → Radians`, `acos(F64) → Radians`, `atan2(F64, F64) → Radians`
- `deg_to_rad(Degrees) → Radians`, `rad_to_deg(Radians) → Degrees`

**Math** (additions to existing math builtins):
- `absf(F64) → F64`, `pow(F64, F64) → F64`

**Composite type constructors/accessors**:
- `timespec(seconds I64, nanos I64) → Timespec`
- `timespec_seconds(Timespec) → I64`, `timespec_nanos(Timespec) → I64`
- `timezone(offset_minutes I64) → Timezone`
- `tz_convert(Timespec, Timezone) → Timespec`
- `duration(nanos I64) → Duration`, `duration_nanos(Duration) → I64`
- `date(year I64, month I64, day I64, tz Timezone) → Date`
- `date_year(Date) → I64`, `date_month(Date) → I64`, `date_day(Date) → I64`
- `point2d(x I64, y I64) → Point2D`, `point2d_x(Point2D) → I64`, etc.
- `point3d(...)`, `pointf2d(...)`, `pointf3d(...)` — same pattern
- `ipv4(a, b, c, d I64) → IPv4`, `ipv4_octet(IPv4, index I64) → I64`
- `ipv6(...)` — from 16 bytes or 8 × uint16
- `priest_id(id I64, name Str) → PriestId`, `priest_id_num(PriestId) → I64`
- `maz_id(id I64, path Str) → MazId`, `maz_id_num(MazId) → I64`

**Collection operations** (extend existing coll_* builtins to all element types):
- `coll_len`, `coll_get`, `coll_take`, `coll_drop`, `coll_sort`, `coll_filter`
  all work on `[]T` for any scalar type T. The element type tag in FlatCollRef
  drives dispatch.

## Estimated Scope

~8 files, ~1200-1800 lines of implementation + tests. The types are simple
(fixed-size structs, bitmap allocators, byte-buffer accessors) but there are 23
of them, each needing constructors, accessors, and round-trip tests. The
complexity is in getting the layout right so that subsequent phases can build on
it without redesign.

## Open Decisions

1. **FlatValue size**: Decided: 32 bytes (8-byte header + 24-byte Data field).
   The 24-byte Data field is required to fit Point3D and PointFloat3D (3 × 8
   bytes). This is larger than the original 24-byte proposal but necessary to
   accommodate the full type inventory without indirection for composite types.

2. **Attribute node size**: 128 bytes (cache-line-aligned) or smaller? 128
   bytes wastes space for simple value attributes but gives good false-sharing
   properties. Decision: start with 128, measure later.

3. **String slot size**: 256 bytes fixed, or variable-length with a header?
   Fixed is simpler and avoids fragmentation. Decision: 256 fixed.

4. **Edge array growth**: What happens when a node needs more dependency edges
   than initially allocated? Options: (a) fixed max edges per node (e.g., 16),
   (b) edge array is append-only with compaction on node deletion. Decision:
   defer — start with append-only, revisit if fragmentation becomes an issue.

5. **Endianness**: The shared pages are accessed by code on the same machine, so
   native endianness is fine. No cross-architecture serialization needed.
