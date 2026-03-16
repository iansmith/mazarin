# Phase 3: Kernel Attribute Management — Tactical Plan

Strategic context: `constraint-vm-strategic-plan.md` (Phase 3)
Namespace design: `constraint-vm-namespace-and-interactors.md` (Parts 1-2)
Flat data types: `constraint-vm-phase1-flat-repr.md`

## Goal

Make the kernel the authority on the attribute graph. The kernel can create,
write, and dirty-propagate attributes in the shared pages. The kernel owns
the namespace trie. Priests interact with the attribute graph exclusively
through syscalls — they cannot write to the shared pages directly (read-only
mapping enforced by page tables).

## Current State

**Phase 1** delivered the flat data representation (`mazarin/vm/flat/`):
- `FlatValue` (32 bytes, 21 type tags)
- `FlatAttrNode` (128 bytes, with Owner, Kind, Flags, SeqCounter, CachedValue,
  DepsOffset/Count, DependentsOffset/Count, NameOffset)
- `PageRegion` with bitmap allocator (nodes, strings) and bump allocator
  (edges, bytecode, collections)
- Flag constants: `FlagDirty`, `FlagEagerNotify`, `FlagTombstoned`

**Phase 2** delivered shared page allocation (`kmazarin/kmem/constraint.go`):
- 128 contiguous pages (512KB) from buddy allocator (order 7)
- Mapped read-only at `0x00007FFD00000000` in every priest
- Kernel writes via `PA + KernelMMIOOffset`
- Header with magic (`0x4D415A46`) and version (1)
- Validated on all 4 platforms

**What Phase 2 deferred** (now Phase 3's responsibility):
- Namespace trie region
- Per-priest scratch pages
- Generation counter in page header
- Initializing the PageRegion layout within the allocated 512KB

## What We're Building

Seven deliverables, roughly in dependency order:

1. **Kernel-side PageRegion initialization** — lay out the flat data regions
   within the 512KB shared pages
2. **Namespace trie** — flat, pointer-free trie over URI segments in a
   dedicated region of the shared pages
3. **SysAttrCreate** — create a value or constraint attribute with a URI;
   kernel inserts into namespace trie
4. **SysAttrWrite** — write a value to a value attribute; panics on constraint
5. **Dirty propagation** — DFS walk over dependency edges; generation counter
   for diamond dedup
6. **SysAttrAddDep / SysAttrUpdateDeps** — manage dependency edges
7. **SysAttrRegisterQuery** — register find patterns; kernel maintains query
   result collection attributes

## Design Decisions

### 1. Kernel-Side PageRegion Initialization

Phase 1's `PageRegion` uses Go slices over a `[]byte` backing buffer. The
kernel cannot use this directly — the shared pages are at a fixed PA accessed
via `PA + KernelMMIOOffset`, not a Go heap allocation.

**Approach**: New kernel-side struct `KernelAttrManager` in
`kmazarin/ksyscall/constraint_mgr.go` that wraps the raw VA pointer and
provides the same operations as `PageRegion` (AllocNode, WriteString,
WriteEdges, etc.) but operates on the shared page memory directly via
`unsafe.Pointer` arithmetic.

```go
type KernelAttrManager struct {
    baseVA   uintptr  // kernel VA of shared pages start
    basePA   uintptr  // PA of shared pages start (for user VA calculation)

    // Region offsets (byte offsets from baseVA)
    nodeRegionOff    uint32
    edgeRegionOff    uint32
    bytecodeRegionOff uint32
    stringRegionOff  uint32
    collRegionOff    uint32
    trieRegionOff    uint32

    // Capacities
    nodeCapacity     uint16  // max attribute slots
    trieCapacity     uint16  // max trie nodes
    stringCapacity   uint16  // max string slots
    edgeCapacity     uint16  // max edges

    // Allocator state
    nodeBitmap       [64]byte  // 512 bits = 512 slots
    stringBitmap     [32]byte  // 256 bits = 256 slots
    trieBitmap       [256]byte // 2048 bits = 2048 trie nodes
    edgeUsed         uint32    // bump pointer for edges
    bytecodeUsed     uint32    // bump pointer for bytecode
    collectionUsed   uint32    // bump pointer for collections

    // Generation counter (also stored in shared page header)
    generation       uint64

    // Dirty walk generation (for diamond dedup)
    walkGen          uint64

    // Query pattern registry (kernel-only, not in shared pages)
    queries          [MaxQueryPatterns]queryPattern
    queryCount       uint16
}
```

This struct lives in kernel BSS (not in the shared pages). It holds allocator
state and the query pattern registry. The shared pages hold only the data
that priests need to read.

**Shared page layout within 512KB**:

```
Offset     Region                Size      Notes
0x0000     Page Header           256B      magic, version, generation, region table
0x0100     Attribute Nodes       64KB      512 × 128B
0x10100    Edge Array            8KB       4096 × uint16
0x12100    Bytecode              32KB      2048 × 16B instructions
0x1A100    String Data           64KB      256 × 256B slots
0x2A100    Collection Data       32KB      1024 × 32B elements
0x32100    Namespace Trie        256KB     2048 × 128B nodes
0x72100    (Free/growth)         ~57KB     future: scratch pages, etc.
Total: ~460KB of 512KB used
```

The page header stores a region table so user-side code (future VDSO) can
discover region offsets:

```go
// SharedPageHeader — first 256 bytes of the shared pages.
// Readable by priests (read-only mapping).
type SharedPageHeader struct {
    Magic            uint32   // 0x4D415A46 "MAZF"
    Version          uint32   // 2 (bumped from 1)
    Generation       uint64   // bumped on attr destroy / trie mutation

    // Region table (offset, capacity pairs — byte offsets from page start)
    NodeRegionOff    uint32
    NodeCapacity     uint16
    _pad0            uint16
    EdgeRegionOff    uint32
    EdgeCapacity     uint16
    _pad1            uint16
    BytecodeRegionOff uint32
    BytecodeCapacity uint16
    _pad2            uint16
    StringRegionOff  uint32
    StringCapacity   uint16
    _pad3            uint16
    CollRegionOff    uint32
    CollCapacity     uint16
    _pad4            uint16
    TrieRegionOff    uint32
    TrieCapacity     uint16
    _pad5            uint16

    // Padding to 256 bytes
    _reserved        [256 - 72]byte
}
```

**Initialization** happens in `InitConstraintPages()` (or a new
`InitConstraintRegions()` called from it) — writes the header with region
offsets and zeroes the bitmaps. This replaces the current minimal
magic+version write.

### 2. Namespace Trie

A flat, pointer-free trie in the shared pages. Each trie node is 128 bytes.

```go
type TrieNode struct {
    Segment      [64]byte  // URI segment, NUL-terminated
    FirstChild   uint16    // index of first child (0xFFFF = none)
    NextSibling  uint16    // index of next sibling (0xFFFF = none)
    AttrSlot     uint16    // attribute slot (0xFFFF = not a leaf)
    ChildCount   uint16    // total descendants (for exists() queries)
    SeqCounter   uint32    // seqlock: odd = mutation in progress
    _pad         [48]byte  // pad to 128 bytes
}
```

The root node (index 0) is always allocated. Its segment is empty (the root
of `attr:///`). Under the root are two children: `"priest"` and `"kernel"`.

**Trie operations** (all kernel-side, all write to shared pages via kernel VA):

- `trieInsert(uri string, attrSlot uint16) error` — parse URI into segments,
  walk/create trie nodes, set AttrSlot on the leaf, increment ChildCount on
  ancestors.
- `trieRemove(uri string) error` — find leaf, clear AttrSlot, decrement
  ChildCount on ancestors. Remove empty branch nodes.
- `trieLookup(uri string) (uint16, bool)` — walk segments, return AttrSlot.
  Used by kernel for SysAttrWrite to find the attribute slot for a URI.
- `trieMatchPattern(pattern string) []uint16` — walk with `*` wildcard
  support. Returns matching attribute slot indices. Used by kernel for
  query pattern evaluation.
- `trieCountChildren(uri string) uint16` — walk to node, return ChildCount.
  Used for `exists()` semantics.

**URI parsing**: Split on `/`, skip the `attr:///` prefix. So
`attr:///priest/calendar.elf/str/eventTitle` becomes segments
`["priest", "calendar.elf", "str", "eventTitle"]`.

**Seqlock protocol**: Before mutation, increment SeqCounter to odd. After
mutation, increment to even. Reader-side (future VDSO) loads counter, reads,
loads again, retries if changed. For Phase 3 we only need the writer side —
the kernel always holds a lock (implicit single-writer since all mutations
go through syscalls serialized by `saveAndDisableIRQs`).

### 3. SysAttrCreate (0x1021)

```
SysAttrCreate(uriBufPtr, uriLen, valueType, attrKind, bytecodePtr, bytecodeLen uint64) → slotIndex int64
```

Arguments:
- `uriBufPtr` + `uriLen`: user buffer containing URI string
- `valueType`: one of the TypeXxx constants (TypeI64, TypeStr, etc.)
- `attrKind`: `AttrKindValue` (0) or `AttrKindConstraint` (1)
- `bytecodePtr` + `bytecodeLen`: user buffer containing bytecode (for
  constraints; 0,0 for values)

Returns: slot index (>= 0) on success, negative errno on failure.

**Implementation steps**:

1. Copy URI string from user buffer (`CopyFromUser`, max 1024 bytes).
2. Validate URI format: must start with `attr:///priest/<name>/` where
   `<name>` matches calling priest's name. Kernel-owned attributes use
   `attr:///kernel/` and are created via a kernel-internal API, not this
   syscall.
3. Allocate a node slot from the bitmap (`AllocNode`).
4. Initialize the `FlatAttrNode`: set Owner to calling priest's ID, Kind,
   ValueType, Flags = `FlagDirty` (constraints start dirty), SeqCounter = 0.
5. If constraint: copy bytecode from user buffer, allocate bytecode region
   space, set ProgramOffset/ProgramLen.
6. Allocate string slot for the URI, store in NameOffset.
7. Insert into namespace trie (`trieInsert`).
8. Check registered query patterns — if any match, add this slot to the
   matching query result collections and dirty-propagate.
9. Return slot index.

**Error codes**:
- `-EFAULT` (-14): invalid user buffer
- `-ENOMEM` (-12): no free slots / regions full
- `-EINVAL` (-22): bad URI format, bad type, bad kind
- `-EEXIST` (-17): URI already registered
- `-EPERM` (-1): priest trying to create under another priest's namespace

### 4. SysAttrWrite (0x1022)

```
SysAttrWrite(slotIndex, valueBufPtr, valueLen uint64, _, _, _ uint64) → 0 or -errno
```

Arguments:
- `slotIndex`: attribute slot index
- `valueBufPtr` + `valueLen`: user buffer containing a `FlatValue` (32 bytes)

Returns: 0 on success, negative errno on failure.

**Implementation steps**:

1. Validate slot index (in range, allocated, not tombstoned).
2. Check ownership: caller's priest ID must match node's Owner.
3. **Enforcement**: if `node.Kind == AttrKindConstraint`, return `-EPERM`.
   This is the fundamental constraint system invariant. (The strategic plan
   says "panic" but in kernel context we return an error. The client library
   in Phase 5 can panic on the user side.)
4. Copy `FlatValue` from user buffer (32 bytes via `CopyFromUser`).
5. Validate value type matches `node.ValueType`.
6. Seqlock: increment SeqCounter to odd. Write CachedValue. Increment
   SeqCounter to even.
7. Dirty propagation: walk this node's dependents, marking dirty. Use
   generation counter for diamond dedup.
8. Return 0.

**Alternative: URI-based write**. The strategic plan says `SysAttrWrite(uri,
value)`. But looking up a URI through the trie on every write is O(segments).
Since the client library will cache the slot index after `SysAttrCreate`,
using slot index directly is O(1). We provide both:

- `SysAttrWrite` (0x1022): by slot index (fast path)
- `SysAttrWriteURI` (0x1023): by URI string (convenience, calls trieLookup
  internally then delegates to SysAttrWrite)

### 5. Dirty Propagation

When a value attribute is Set(), or a constraint's dependency changes, the
kernel walks the dependency graph marking nodes dirty.

```go
func (mgr *KernelAttrManager) dirtyPropagate(startSlot uint16) {
    mgr.walkGen++
    mgr.dirtyWalk(startSlot, mgr.walkGen)
}

func (mgr *KernelAttrManager) dirtyWalk(slot uint16, gen uint64) {
    node := mgr.node(slot)

    // Diamond dedup: skip if already visited in this walk.
    if node.lastWalk == gen {
        return
    }
    node.lastWalk = gen

    // Mark dirty.
    node.SetDirty(true)

    // Walk dependents.
    for i := 0; i < int(node.DependentsCount); i++ {
        depSlot := mgr.readEdge(node.DependentsOffset, i)
        mgr.dirtyWalk(depSlot, gen)
    }
}
```

**Diamond dedup**: The `lastWalk` field prevents exponential blowup when
multiple paths converge on the same node. It uses a per-walk generation
counter, matching the existing heap-based algorithm in `mazarin/attr/attr.go`.

**Where does lastWalk live?** It's kernel-only state (not needed by priests).
Options:
- (a) Use some of FlatAttrNode's 60-byte `_pad2` field. Pro: co-located
  with the node, good cache behavior. Con: wastes shared page space on
  kernel-only data.
- (b) Separate kernel-side array `lastWalk [512]uint64`.

**Decision**: (a) — use 8 bytes of `_pad2` for lastWalk. It's read-only
noise from the priest's perspective, but keeping it co-located means dirty
walks touch fewer cache lines. This changes `_pad2` from `[60]byte` to
`LastWalk uint64` + `_pad2 [52]byte`. This requires updating `flat/node.go`
to add the field.

**Eager notification**: If `FlagEagerNotify` is set, add the node to an
eager-eval list during the dirty walk. After the walk completes, evaluate
eager nodes. This is implemented in Phase 6 (dirty notification), but the
walk collects them now so the infrastructure is ready.

### 6. SysAttrAddDep (0x1024) and SysAttrUpdateDeps (0x1025)

**SysAttrAddDep** — add a single dependency edge:

```
SysAttrAddDep(fromSlot, toSlot uint64, _, _, _, _ uint64) → 0 or -errno
```

- `fromSlot`: the constraint that depends on `toSlot`
- `toSlot`: the attribute being depended on

Implementation:
1. Validate both slots (allocated, not tombstoned).
2. Allocate edge array entries for both forward (fromSlot.Deps) and reverse
   (toSlot.Dependents) edges.
3. **Edge array management**: Since edges are bump-allocated, adding a dep
   means we need to either (a) append to the existing edge list (requires
   contiguous space after current edges) or (b) allocate a new block and
   copy. For simplicity in Phase 3: allocate a new edge block, copy old
   edges + new edge, update offset/count. The old edge block is wasted
   (fragmentation). Phase 13 (cleanup) can compact.

**SysAttrUpdateDeps** — atomically replace the full dependency set:

```
SysAttrUpdateDeps(constraintSlot, readSetBufPtr, readSetCount uint64, _, _, _ uint64) → 0 or -errno
```

- `constraintSlot`: the constraint whose deps are changing
- `readSetBufPtr` + `readSetCount`: user buffer with uint16 slot indices
  (the new read set from evaluation)

Implementation:
1. Copy new read set from user buffer.
2. Compare with current deps. If identical, return 0 (no-op fast path).
3. Remove this constraint from old deps' dependents lists.
4. Add this constraint to new deps' dependents lists.
5. Update forward edges (deps) on the constraint node.

**Removing from dependents**: Walk the old deps. For each, scan its
dependents list for `constraintSlot` and remove it. Since edge arrays are
bump-allocated and fixed-size per allocation, "removing" means copying the
list minus the removed entry into a new allocation. This is O(n) per dep
but n is small (typical constraint has < 10 deps).

### 7. SysAttrRegisterQuery (0x1026)

```
SysAttrRegisterQuery(patternBufPtr, patternLen uint64, _, _, _, _ uint64) → slotIndex int64
```

Registers a `find` pattern and returns a query result attribute (a collection
of URI strings).

**Implementation steps**:

1. Copy pattern string from user buffer.
2. Check if pattern already registered (dedup).
3. Create a new attribute slot: Owner = calling priest, Kind = AttrKindValue,
   ValueType = TypeCollection (element type = TypeStr).
4. Evaluate the pattern against the current namespace trie
   (`trieMatchPattern`).
5. Build a collection of URI strings from matching trie leaves.
6. Store the collection in the collection data region.
7. Write the collection reference as the attribute's CachedValue.
8. Store the pattern in the kernel-side query registry (not in shared pages).
9. Link the query result slot: it doesn't have explicit deps, but the kernel
   knows to re-evaluate it on every `SysAttrCreate` / destroy that matches
   the pattern.
10. Return the query result attribute's slot index.

**Pattern matching**: Patterns use `*` as a single-segment wildcard.
`attr:///priest/*/str/eventTitle` matches any priest's `str/eventTitle`.
Multi-segment wildcards (`**`) are not supported.

**Update on namespace change**: When `SysAttrCreate` or `trieRemove` is
called, the kernel iterates registered query patterns. For each pattern
that could match the changed URI (fast reject: compare non-wildcard
segments), re-evaluate and update the query result collection. If the
collection changed, dirty-propagate from the query result slot.

**Capacity**: `MaxQueryPatterns = 64`. Query patterns are stored in
kernel-only memory (the `KernelAttrManager` struct), not in shared pages.

### 8. Ownership Tracking

Each `FlatAttrNode.Owner` stores the priest ID (uint16). Priest ID 0 is
reserved for the kernel.

**Enforcement in syscalls**:
- `SysAttrCreate`: sets Owner to calling priest's ID
- `SysAttrWrite`: verifies caller owns the attribute
- `SysAttrAddDep`: any priest can add a dependency (read access is universal)
- `SysAttrRegisterQuery`: sets Owner on the query result attribute

**Priest ID lookup**: The kernel already tracks the current priest's ID in
the thread/scheduler state. The syscall handlers access it via the existing
`currentPriestID()` function (or equivalent).

### 9. Kernel-Internal Attribute Creation

For Phase 7 (kernel-published attributes like time, mouse, screen), the
kernel creates attributes without going through the syscall path. The
`KernelAttrManager` provides internal APIs:

```go
func (mgr *KernelAttrManager) CreateKernelAttr(uri string, valueType uint8) (uint16, error)
func (mgr *KernelAttrManager) SetKernelAttr(slot uint16, value FlatValue)
```

These bypass ownership checks (kernel is always owner 0) and URI prefix
validation. They use the same trie insertion and dirty propagation logic.

Not implemented in Phase 3, but the `KernelAttrManager` API is designed to
support them.

## Package Structure

### New Files

| File | Package | Contents |
|---|---|---|
| `kmazarin/ksyscall/constraint_mgr.go` | ksyscall | KernelAttrManager struct, initialization, node/string/edge/trie allocation |
| `kmazarin/ksyscall/constraint_trie.go` | ksyscall | TrieNode, trie insert/remove/lookup/match, URI parsing |
| `kmazarin/ksyscall/constraint_dirty.go` | ksyscall | Dirty propagation walk, generation counter |
| `kmazarin/ksyscall/constraint_syscall.go` | ksyscall | SysAttrCreate, SysAttrWrite, SysAttrWriteURI, SysAttrAddDep, SysAttrUpdateDeps, SysAttrRegisterQuery |
| `mazarin/sys/constraint.go` | sys | Client-side syscall wrappers (AttrCreate, AttrWrite, etc.) |

### Modified Files

| File | Change |
|---|---|
| `mazarin/vm/flat/node.go` | Add `LastWalk uint64` field, reduce `_pad2` to `[52]byte` |
| `kmazarin/ksyscall/mazzy.go` | Add syscall numbers 0x1021-0x1026, dispatch table entries |
| `kmazarin/kmem/constraint.go` | Expand `InitConstraintPages` to initialize region layout and header |
| `mazarin/sys/syscall.go` | Add client-side syscall number constants |

### Not Modified

- `mazarin/vm/flat/` (types.go, value.go, composite.go, alloc.go, layout.go,
  convert.go) — Phase 1 code stays as-is. The kernel-side manager reimplements
  the allocator logic for raw memory access.
- `mazarin/attr/` — the heap-based attribute system is separate from the
  shared-page system. They coexist; the heap-based system is used by the
  compiler and tests.

## Syscall Number Summary

| Number | Name | Purpose |
|---|---|---|
| 0x1021 | SysAttrCreate | Create attribute with URI |
| 0x1022 | SysAttrWrite | Write value by slot index |
| 0x1023 | SysAttrWriteURI | Write value by URI string |
| 0x1024 | SysAttrAddDep | Add single dependency edge |
| 0x1025 | SysAttrUpdateDeps | Replace full dependency set |
| 0x1026 | SysAttrRegisterQuery | Register find pattern, get query result slot |

## Implementation Order

### Step 1: Shared Page Header and Region Layout

Modify `kmazarin/kmem/constraint.go`:
- Define `SharedPageHeader` struct (update magic version to 2)
- Compute region offsets and write them into the header during
  `InitConstraintPages`
- Zero all regions

Add `kmazarin/ksyscall/constraint_mgr.go`:
- `KernelAttrManager` struct with kernel-VA-based accessors
- `InitKernelAttrManager()` — called once after `InitConstraintPages`,
  reads header to discover region offsets, initializes bitmaps
- `node(slot uint16) *FlatAttrNode` — returns writable pointer into
  shared pages via kernel VA
- `allocNode() (uint16, error)` — bitmap allocator
- `writeString(s string) (FlatStrRef, error)` — string slot allocator
- `writeEdges(slots []uint16) (uint32, error)` — bump allocator
- Global `var attrMgr KernelAttrManager`

**Test**: Build on all 3 architectures. Run on one — verify header is written
with correct magic/version/offsets. Read the header from a priest (via the
fixed VA) and verify magic.

### Step 2: Namespace Trie

Add `kmazarin/ksyscall/constraint_trie.go`:
- `TrieNode` struct (128 bytes, matches the design doc)
- `parseURI(uri string) (segments []string, err error)` — split URI into
  segments, validate format
- `trieInsert(uri string, attrSlot uint16) error`
- `trieLookup(uri string) (uint16, bool)`
- `trieRemove(uri string) error`
- `trieMatchPattern(pattern string, results []uint16) int` — writes matches
  into caller-provided buffer, returns count

Initialize root node (slot 0) with empty segment. Allocate "priest" and
"kernel" children during init.

**Test**: Create attributes with URIs, look them up, remove them, verify
trie structure. Test wildcard matching.

### Step 3: SysAttrCreate

Add to `kmazarin/ksyscall/constraint_syscall.go`:
- `SyscallAttrCreate(uriBuf, uriLen, valueType, attrKind, bytecodeBuf, bytecodeLen uint64) int64`
- Register in `mazzySyscallTable[0x21]`

Add to `mazarin/sys/constraint.go`:
- `AttrCreate(uri string, valueType uint8, kind uint8, bytecode []byte) (uint16, error)`

**Test**: Call SysAttrCreate from a priest, verify slot is allocated, URI is
in the trie, node fields are correct.

### Step 4: SysAttrWrite + Dirty Propagation

Add to `kmazarin/ksyscall/constraint_syscall.go`:
- `SyscallAttrWrite(slotIndex, valueBuf, valueLen, _, _, _ uint64) int64`
- `SyscallAttrWriteURI(uriBuf, uriLen, valueBuf, valueLen, _, _ uint64) int64`
- Register in dispatch table

Add `kmazarin/ksyscall/constraint_dirty.go`:
- `dirtyPropagate(startSlot uint16)`
- `dirtyWalk(slot uint16, gen uint64)`

Modify `mazarin/vm/flat/node.go`:
- Add `LastWalk uint64` to FlatAttrNode (offset 68, using first 8 bytes of
  `_pad2`)

**Test**: Create two value attributes and one constraint attribute. Set up
deps: constraint depends on both values. Write to one value, verify
constraint is marked dirty.

### Step 5: SysAttrAddDep + SysAttrUpdateDeps

Add to `kmazarin/ksyscall/constraint_syscall.go`:
- `SyscallAttrAddDep(fromSlot, toSlot, _, _, _, _ uint64) int64`
- `SyscallAttrUpdateDeps(constraintSlot, readSetBuf, readSetCount, _, _, _ uint64) int64`
- Helper: `addForwardEdge(fromSlot, toSlot)`, `addReverseEdge(toSlot, fromSlot)`
- Helper: `removeReverseEdge(depSlot, constraintSlot)`

**Test**: Create constraint with deps. Call UpdateDeps to change deps.
Verify old reverse edges removed, new ones added. Verify dirty propagation
follows new edges.

### Step 6: SysAttrRegisterQuery

Add to `kmazarin/ksyscall/constraint_syscall.go`:
- `SyscallAttrRegisterQuery(patternBuf, patternLen, _, _, _, _ uint64) int64`
- `queryPattern` struct: `{pattern [256]byte, patternLen uint16, resultSlot uint16, ownerPriest uint16}`
- `updateQueryResults()` — called from SysAttrCreate and future trieRemove

**Test**: Register query pattern `attr:///priest/*/str/eventTitle`. Create
a matching attribute. Verify query result collection is updated. Verify
dirty propagation from query result slot.

## Edge Cases and Robustness

### Memory pressure
- All allocations can fail (bitmap full, bump full). Syscalls return
  `-ENOMEM`.
- The kernel does NOT grow the shared pages dynamically in Phase 3. 512KB
  is the fixed budget. If it fills up, syscalls fail. Growth is a Phase 10+
  concern.

### Concurrent access
- Phase 3 is single-core. All syscalls run with IRQs disabled via
  `saveAndDisableIRQs()` (same pattern as TransferPages). No lock
  contention.
- Seqlocks are written (odd→even transitions) during mutations. No reader
  code yet (future VDSO). But the protocol is established.
- Phase 10 adds multi-core seqlock readers.

### URI validation
- Must start with `attr:///`
- `attr:///priest/<name>/...` — `<name>` must match calling priest
- `attr:///kernel/...` — only kernel-internal API, not via syscall
- Segments: no empty segments, no `/` within segments, max segment length 63
  bytes (trie node segment field is 64 bytes including NUL)
- Max URI length: 1024 bytes
- Max segments: 16 (prevents absurdly deep trie walks)

### Edge array fragmentation
- Edges are bump-allocated. When deps change, old edge blocks are abandoned.
- Fragmentation accumulates over time. For Phase 3 this is acceptable —
  the edge region is 8KB (4096 edges) and dependency changes are infrequent
  (only when `find`/`deref` read sets change).
- Compaction can be added in Phase 13 (cleanup).

### Dirty walk depth
- DFS can recurse deeply if the dependency graph is tall. Stack depth =
  graph depth. With 512 attribute slots and practical UI graphs, depth > 20
  is unlikely.
- If needed: convert to iterative with an explicit stack array. Not
  necessary for Phase 3.

## What This Phase Does NOT Include

- No VM interpreter changes (Phase 4)
- No client library (Phase 5) — only raw syscall wrappers
- No eager notification / channels (Phase 6)
- No kernel-published attributes (Phase 7)
- No VDSO reader code (Phase 11)
- No multi-core synchronization beyond seqlock writes (Phase 10)
- No attribute destruction / tombstoning (Phase 13)
- No per-priest scratch pages (deferred to Phase 4 where they're needed
  for read set tracking during VM evaluation)
- No constraint evaluation — Phase 3 manages the graph structure and dirty
  flags, but does NOT evaluate constraint bytecode. Evaluation is Phase 4.

## Testing Strategy

Phase 3 code lives in the kernel and requires syscalls to exercise. Testing
approaches:

1. **Build verification**: `$GO tool task` succeeds on all 3 architectures.
   This catches compilation errors, type mismatches, and linking issues.

2. **Boot test**: `$GO tool task run` — kernel boots with initialized
   constraint page header. Serial output confirms region offsets.

3. **Syscall smoke test**: A test priest (or a section in an existing priest
   like helloworld) that:
   - Calls SysAttrCreate to create a value attribute
   - Calls SysAttrWrite to set its value
   - Reads the shared page to verify the value appears
   - Creates a second attribute and adds a dependency
   - Writes to the first attribute and verifies the second is marked dirty
   - Registers a query pattern and verifies the query result collection

4. **URI validation test**: Try creating attributes with bad URIs (missing
   prefix, wrong priest name, empty segments, too-long segments, duplicate
   URIs) and verify correct error codes.

5. **Capacity test**: Allocate attributes until slots are exhausted, verify
   `-ENOMEM` is returned. Free some, verify reuse works. (This tests the
   bitmap allocator under kernel conditions.)

## Open Decisions

1. **Per-priest scratch pages**: The strategic plan mentions these for
   resolution caches and read sets during evaluation. They're not needed
   until Phase 4 (VM evaluation) or Phase 11 (VDSO). Deferred.

2. **Edge compaction**: When to compact the edge array? Phase 13 (cleanup)
   seems right. For Phase 3, live with fragmentation.

3. **Constraint evaluation trigger**: Phase 3 marks nodes dirty but doesn't
   evaluate them. Who triggers evaluation? In Phase 4, the VM evaluates on
   Get(). In Phase 5, the client library's `handle.Get()` calls the
   interpreter. Phase 3 just sets the dirty flag.

4. **Query pattern limit**: 64 patterns. Is this enough? For Phase 3, yes.
   The interactor framework (Phase 8) may need more if each interactor type
   registers patterns. Can increase later.

5. **Namespace trie node limit**: 2048 nodes. A simple UI with 100
   interactors × ~15 attributes each = 1500 URIs, each with ~5 segments =
   ~3000 trie nodes (with sharing). 2048 might be tight for complex UIs.
   Can increase by reducing other regions or growing the shared pages.
   For Phase 3 this is fine.
