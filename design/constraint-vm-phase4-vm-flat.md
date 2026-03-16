# Constraint VM Phase 4: VM Interpreter on Flat Representation

Ian Smith, March 2026

Tactical plan for adapting the VM interpreter to work with flat shared-page data,
adding new builtins, and implementing dynamic read set tracking.

## Design Decision: Boundary Conversion (Option C — Hybrid)

**The interpreter continues to operate on `vm.Value` internally.** FlatValue is
converted to vm.Value at read time (when the VM reads an attribute from shared
pages) and vm.Value is converted to FlatValue at write time (when evaluation
results are stored back).

**Why not operate on FlatValue directly?**

- The interpreter loop and all existing tests work with vm.Value. Rewriting to
  FlatValue would be a major upheaval for marginal benefit.
- FlatValue strings are references (offset+len into a string region) — arithmetic
  on them requires access to the string region. vm.Value strings are Go strings,
  which are self-contained. Keeping Go strings in the interpreter avoids threading
  a region pointer through every string operation.
- Collections in FlatValue are similarly region-referenced. vm.Value collections
  are `[]Value` — natural for iteration, take/drop/sort.
- The conversion cost is minimal: constraint programs read a handful of attributes
  per evaluation (typically 1-20). The per-attribute cost of a 32-byte copy +
  field extraction is negligible compared to the trie walk or syscall overhead.

**What changes in vm.Value:**

vm.Value gets a `data [24]byte` field for composite types (Rectangle, Timespec,
Point2D, etc.). These types store their fields in the same byte layout as
FlatValue.Data, so conversion is a straight memcpy. The existing `i64`, `f64`,
`str`, and `coll` fields remain for their respective types.

New type tags in vm.Value (matching flat.Type* constants):

```
TypeTimespec  = 0x06    TypePointF2D  = 0x0C
TypeTimezone  = 0x07    TypePointF3D  = 0x0D
TypeDuration  = 0x08    TypeRadians   = 0x0E
TypeDate      = 0x09    TypeDegrees   = 0x0F
TypePoint2D   = 0x0A    TypeIPv4      = 0x10
TypePoint3D   = 0x0B    TypeIPv6      = 0x11
                        TypePriestId  = 0x12
                        TypeMazId     = 0x13
                        TypeRectangle = 0x15
```

The first 5 type tags (I64=1, F64=2, Bool=3, Tribool=4, Str=5) are already
shared between vm.Value and FlatValue by design.

## Step 1: Extend vm.Value with Composite Types

**File: `mazarin/vm/value.go`**

Add:
- `data [24]byte` field to the `Value` struct
- New type tag constants (0x06-0x15) matching `flat.Type*`
- Constructor functions: `Rectangle(x0, y0, x1, y1 int32) Value`,
  `TimespecVal(sec int64, nanos int32) Value`, `Point2DVal(x, y int64) Value`,
  `PointF2DVal(x, y float64) Value`, `DurationVal(nanos int64) Value`,
  `DateVal(year int16, month, day uint8, tz int16) Value`, etc.
- Accessor methods: `AsRectangle() (x0, y0, x1, y1 int32)`,
  `AsTimespec() (int64, int32)`, `AsPoint2D() (int64, int64)`, etc.
- `TypeName()` and `String()` updates for all new types.

**File: `mazarin/vm/convert.go`** (new)

Bridge functions:
```go
func ValueFromFlat(fv flat.FlatValue, strRegion []byte) Value
func ValueToFlat(v Value) flat.FlatValue
```

- For scalars (I64, F64, Bool, Tribool): direct field copy.
- For strings: `ValueFromFlat` reads the string data from `strRegion` using the
  FlatStrRef offset+len. `ValueToFlat` is not needed for strings in Phase 4
  (constraint results are written via the existing SysAttrWrite path).
- For composites (Rectangle, Timespec, etc.): copy the 24-byte `Data` array
  directly — the byte layout is identical by construction.
- For collections: `ValueFromFlat` reads element FlatValues from the collection
  region and converts each recursively. Collections of scalars only (no nested
  collections in flat representation).

**Tests:** Unit tests for round-trip conversion of every type.

## Step 2: Rectangle Builtins

**File: `mazarin/vm/builtin.go`**

New builtin IDs (60-68):
```
BuiltinRect           = 60  // (I64, I64, I64, I64) → Rectangle
BuiltinRectUnion      = 61  // (Rect, Rect) → Rect
BuiltinRectIntersect  = 62  // (Rect, Rect) → Rect
BuiltinRectOverlaps   = 63  // (Rect, Rect) → Bool
BuiltinRectContains   = 64  // (Rect, Rect) → Bool
BuiltinRectEmpty      = 65  // (Rect) → Bool
BuiltinRectArea       = 66  // (Rect) → I64
BuiltinRectWidth      = 67  // (Rect) → I64
BuiltinRectHeight     = 68  // (Rect) → I64
```

Implementation:
- `rect(x0, y0, x1, y1)`: pop 4 I64, construct Rectangle (normalize: ensure
  x0<=x1, y0<=y1)
- `rect_union`: bounding box (min of mins, max of maxes). Empty sentinel if
  either input is empty.
- `rect_intersect`: max of mins, min of maxes. Returns empty rect if no overlap.
- `rect_overlaps`: true if intersection is non-empty.
- `rect_contains(outer, inner)`: true if inner is entirely within outer.
- `rect_empty`: true if width<=0 or height<=0.
- `rect_area`: width * height (0 if empty).
- `rect_width`, `rect_height`: x1-x0, y1-y0 (0 if negative).

These use `int32` internally (matching FlatValue layout) but are pushed/popped
as I64 arguments in the instruction stream and converted to int32 inside the
builtin. This avoids adding an I32 type to the VM.

**Tests:** Comprehensive tests for each rect builtin, including edge cases
(empty rects, degenerate, overflow).

## Step 3: Trig Builtins

**File: `mazarin/vm/builtin.go`**

New builtin IDs (70-79):
```
BuiltinSin      = 70  // (F64) → F64
BuiltinCos      = 71  // (F64) → F64
BuiltinTan      = 72  // (F64) → F64
BuiltinAsin     = 73  // (F64) → F64
BuiltinAcos     = 74  // (F64) → F64
BuiltinAtan2    = 75  // (F64, F64) → F64
BuiltinDegToRad = 76  // (F64) → F64
BuiltinRadToDeg = 77  // (F64) → F64
BuiltinAbsF     = 78  // (F64) → F64
BuiltinPow      = 79  // (F64, F64) → F64
```

Implementation: thin wrappers around `math.Sin`, `math.Cos`, etc. `deg_to_rad`
multiplies by `math.Pi/180`, `rad_to_deg` by `180/math.Pi`.

**Tests:** Spot-check each against known values (sin(0)=0, cos(0)=1, etc.).

## Step 4: Composite Constructor/Accessor Builtins

**File: `mazarin/vm/builtin.go`**

New builtin IDs (100-149):
```
// Timespec
BuiltinTimespec        = 100  // (I64, I64) → Timespec  (sec, nanos)
BuiltinTimespecSeconds = 101  // (Timespec) → I64
BuiltinTimespecNanos   = 102  // (Timespec) → I64

// Timezone
BuiltinTimezone        = 103  // (I64) → Timezone  (offset minutes)

// TzConvert
BuiltinTzConvert       = 104  // (Timespec, Timezone) → Timespec

// Duration
BuiltinDuration        = 105  // (I64) → Duration  (nanos)
BuiltinDurationNanos   = 106  // (Duration) → I64

// Date
BuiltinDate            = 107  // (I64, I64, I64) → Date  (year, month, day)
BuiltinDateYear        = 108  // (Date) → I64
BuiltinDateMonth       = 109  // (Date) → I64
BuiltinDateDay         = 110  // (Date) → I64

// Point2D (integer)
BuiltinPoint2D         = 111  // (I64, I64) → Point2D
BuiltinPoint2DX        = 112  // (Point2D) → I64
BuiltinPoint2DY        = 113  // (Point2D) → I64

// Point3D (integer)
BuiltinPoint3D         = 114  // (I64, I64, I64) → Point3D

// PointF2D (float)
BuiltinPointF2D        = 115  // (F64, F64) → PointF2D

// PointF3D (float)
BuiltinPointF3D        = 116  // (F64, F64, F64) → PointF3D

// IPv4
BuiltinIPv4            = 120  // (I64, I64, I64, I64) → IPv4
BuiltinIPv4Octet       = 121  // (IPv4, I64) → I64  (octet index 0-3)

// IPv6
BuiltinIPv6            = 122  // (CollI64) → IPv6  (16-element collection)

// PriestId
BuiltinPriestId        = 130  // (I64) → PriestId  (numeric id)
BuiltinPriestIdNum     = 131  // (PriestId) → I64

// MazId
BuiltinMazId           = 132  // (I64) → MazId  (numeric id)
BuiltinMazIdNum        = 133  // (MazId) → I64
```

Each constructor pops arguments, creates a vm.Value with the composite's data
layout, and pushes. Each accessor pops the composite, extracts the field, and
pushes the scalar result.

**Tests:** Round-trip for each: construct, then extract fields.

## Step 5: AttrResolver Interface

**File: `mazarin/vm/resolve.go`** (new)

The VM needs to interact with the attribute namespace for `find`, `deref`, and
`exists`. Rather than coupling the interpreter to the kernel's shared page
layout, define an interface:

```go
// AttrResolver provides attribute namespace access to the VM interpreter.
// The real implementation reads from shared pages via VDSO or syscall.
// Test implementations can use in-memory maps.
type AttrResolver interface {
    // Find matches a URI pattern against the namespace.
    // Returns matching URIs as a string collection.
    Find(pattern string) []string

    // Deref resolves a URI to a typed value.
    // Returns (value, true) if found with matching type, or (zero, false).
    Deref(uri string, expectedType uint8) (Value, bool)

    // Exists returns true if any attributes exist under the URI prefix.
    Exists(prefix string) bool
}
```

The `machine` struct gets an optional `resolver AttrResolver` field and a
`readSet` for tracking. `Run()` gains an option for passing a resolver:

```go
func RunWithResolver(prog *Program, resolver AttrResolver, args ...Value) ([]Value, *ReadSet, error)
```

The existing `Run()` function remains unchanged (no resolver = service discovery
builtins halt with "no resolver configured").

## Step 6: Service Discovery Builtins

**File: `mazarin/vm/builtin.go`**

New builtin IDs (200-209):
```
BuiltinFind       = 200  // (Str) → CollStr
BuiltinDerefI64   = 201  // (Str) → I64 or halt
BuiltinDerefStr   = 202  // (Str) → Str or halt
BuiltinDerefBool  = 203  // (Str) → Bool or halt
BuiltinDerefF64   = 204  // (Str) → F64 or halt
BuiltinDerefRect  = 205  // (Str) → Rectangle or halt
BuiltinDerefPoint2D = 206 // (Str) → Point2D or halt
BuiltinDerefTribool = 207 // (Str) → Tribool
BuiltinExists     = 208  // (Str) → Bool
BuiltinURISegment = 209  // (Str, I64) → Str
BuiltinIsUnknown  = 210  // (any) → Bool
```

Implementation:

- **find(pattern)**: Calls `resolver.Find(pattern)`, converts result to
  `CollStr`, records the query result in the read set.
- **deref_\<type\>(uri)**: Calls `resolver.Deref(uri, expectedType)`. On success,
  pushes the value. On failure (URI not found or type mismatch), pushes a
  tribool(unknown) sentinel. Records the resolved slot in the read set.
- **exists(prefix)**: Calls `resolver.Exists(prefix)`, pushes Bool result.
  Records the prefix node in the read set.
- **uri_segment(uri, index)**: Pure string operation — parses the URI and
  extracts the segment at the given index. No resolver needed.
- **is_unknown(value)**: Pops a value, pushes true if it's a tribool with value
  `TriboolUnknown`. This is how programs check for failed deref.

**Return convention for deref**: When the URI doesn't resolve, deref pushes
`Tribool(TriboolUnknown)` instead of halting. The program is expected to check
with `is_unknown`. This matches the design doc ("returns value or unknown").

## Step 7: Dynamic Read Set Tracking

**File: `mazarin/vm/readset.go`** (new)

```go
const MaxReadSetSize = 256

type ReadSet struct {
    Slots [MaxReadSetSize]uint16
    Count uint16
}

func (rs *ReadSet) Add(slot uint16) {
    // Dedup: check if already present.
    for i := uint16(0); i < rs.Count; i++ {
        if rs.Slots[i] == slot {
            return
        }
    }
    if rs.Count < MaxReadSetSize {
        rs.Slots[rs.Count] = slot
        rs.Count++
    }
}

func (rs *ReadSet) Equal(other *ReadSet) bool { ... }
```

The `machine` struct gets a `readSet ReadSet` field. Service discovery builtins
(`find`, `deref_*`, `exists`) call `m.readSet.Add(slot)` when they resolve an
attribute.

`RunWithResolver` returns the read set alongside the results. The caller
(client library or evaluation loop) compares it with the previous read set
and calls `SysAttrUpdateDeps` if it changed.

## Step 8: Verifier Updates

**File: `mazarin/vm/verify.go`**

- Add new type tags to `verifyBuiltin` for all new builtins:
  - Rectangle builtins: pop/push correct types
  - Trig builtins: pop F64, push F64
  - Composite constructors: pop scalars, push composite type tag
  - Composite accessors: pop composite, push scalar
  - `find`: pop Str, push CollStr
  - `deref_*`: pop Str, push the target type OR TriboolUnknown. Since the
    verifier is conservative, push the target type tag (the runtime handles
    unknown via is_unknown check).
  - `exists`: pop Str, push Bool
  - `uri_segment`: pop Str + I64, push Str
  - `is_unknown`: pop any, push Bool

- Add fuel accounting for trie walks: the verifier doesn't track actual trie
  size, but the fuel limit on the interpreter already bounds all computation.
  No verifier change needed for this — the existing fuel mechanism covers it.

## Step 9: Tests

**File: `mazarin/vm/builtin_test.go`** — extend with:
- Rectangle builtin tests (construct, union, intersect, overlaps, contains,
  empty, area, width, height)
- Trig builtin tests (sin, cos, tan at known values)
- Composite constructor/accessor round-trip tests

**File: `mazarin/vm/resolve_test.go`** (new) — service discovery tests:
- MockResolver implementing AttrResolver with an in-memory attribute map
- Test find with wildcards
- Test deref with type match/mismatch
- Test exists with/without children
- Test uri_segment extraction
- Test is_unknown detection
- Test read set tracking: verify slots recorded during evaluation
- Test read set comparison: same → no update, different → update

**File: `mazarin/vm/convert_test.go`** (new) — FlatValue ↔ vm.Value:
- Round-trip every type tag
- String conversion with mock region
- Collection conversion

## Implementation Order

1. **Step 1** (vm.Value extension) — foundation, everything depends on this
2. **Step 2** (Rectangle builtins) + **Step 3** (Trig) — independent, can parallelize
3. **Step 4** (Composite builtins) — depends on Step 1
4. **Step 1b** (convert.go) — depends on Step 1
5. **Step 5** (AttrResolver interface) — independent of builtins
6. **Step 6** (Service discovery builtins) — depends on Steps 5, 7
7. **Step 7** (Read set) — small, can do with Step 5
8. **Step 8** (Verifier) — after all builtins are defined
9. **Step 9** (Tests) — incremental with each step

## What This Does NOT Include

- **VDSO trie walker** — that's Phase 5 (client library). Phase 4 uses the
  AttrResolver interface; the real shared-page implementation comes later.
- **Resolution cache** — optimization for Phase 5.
- **Compiler updates** (`mazarin/vm/compile/`) — the compiler generates bytecode
  from Go expressions. Adding new builtin support to the compiler is Phase 5.
- **Shared page region management for collections** — noted as deferred in
  `SyscallAttrRegisterQuery`. Phase 4 tests use mock data.
- **Interactor framework** — Phase 7+ per the strategic plan.

## Invariants

- Existing heap-based VM tests MUST continue to pass unchanged.
- `mazarin/attr/inmem/` is NOT modified (frozen reference copy).
- No new opcodes are added — all new functionality uses `OpCallBuiltin` with
  new builtin IDs. This keeps the instruction encoding stable.
- The `data [24]byte` field in vm.Value is zero for non-composite types —
  existing constructors (I64, F64, Bool, etc.) don't touch it.
