# Constraint VM Phase 5B — Integration Testing & Handle[T] Runtime Validation

## Context

Phase 5A built the client-side `Handle[T]` API in `mazarin/attr/` with typed
constructors, seqlock shared-page reads, Program serialization, and two new
kernel syscalls (`SysAttrWriteResult` 0x1027, `SysAttrWriteString` 0x1028).
All code compiles on all 3 architectures and the existing in-process `attrtest`
priest passes 120s stability on all 4 platforms.

**However, the Handle[T] API has NOT been exercised at runtime.** The attrtest
priest still uses the pure Go in-process `attr.Attribute[T]` — not the new
kernel-managed `Handle[T]` that goes through shared pages and syscalls.

## What Was Built (Phase 5A)

### New kernel syscalls
- `SysAttrWriteResult (0x1027)` — writes constraint evaluation result to a
  constraint slot. Clears dirty, no dirty-propagation. Ownership checked.
- `SysAttrWriteString (0x1028)` — writes string value via kernel-side string
  slot allocation. Supports both value writes (with propagation) and constraint
  result writes (no propagation). Ownership checked.

### Client library (`mazarin/attr/`)
- `handle_init.go` — `Init()` reads SharedPageHeader at `0x00007FFD00000000`,
  creates read-only `PageRegion` via `NewPageRegionFromSharedMapping`
- `handle.go` — `Handle[T]` with `Get()`, `Set()`, `IsDirty()`, `SetEager()`,
  seqlock reads, constraint evaluation via `vm.RunWithResolver`, dependency
  update via `SysAttrUpdateDeps`
- `handle_value.go` — `ValueI64`, `ValueF64`, `ValueBool`, `ValueStr`,
  `ValueTribool`, `ValueComposite`, `ValueRectangle`, `ValuePoint2D`,
  `ValueTimespec`
- `handle_constraint.go` — `ConstraintI64`, `ConstraintF64`, `ConstraintBool`,
  `ConstraintStr`, `ConstraintComposite`
- `handle_resolve.go` — `sharedResolver` implementing `vm.AttrResolver` backed
  by shared-page trie walks and seqlock reads
- `handle_find.go` — `Find()`, `Exists()`
- `handle_program.go` — `MustGetProgram()` stub

### Shared types
- `shared/constants/trie_node.go` — `TrieNode` struct shared between kernel and
  userspace (was in `kmazarin/ksyscall/constraint_trie.go`)

### Program serialization (`mazarin/vm/marshal.go`)
- `Program.Marshal() []byte` — MZBC header + instructions + string table + func table
- `UnmarshalProgram([]byte) *Program` — deserialization with validation
- Tests in `marshal_test.go` — round-trip, simple program, error cases

## What Needs To Happen (Phase 5B)

### 1. Update `SysAttrCreate` to accept full serialized Programs

Currently `SyscallAttrCreate` in `kmazarin/ksyscall/constraint_syscall.go:107`
stores bytecode as raw instruction bytes and sets `ProgramLen` as instruction
count:
```go
node.ProgramLen = uint16(bytecodeLen / 16) // instructions are 16 bytes
```

The new `Program.Marshal()` format includes a MZBC header, string table, and
func table — not just raw instructions. Two options:

**Option A (recommended)**: Store the entire serialized blob in the bytecode
region as-is. Change `ProgramLen` semantics to mean "total bytes" instead of
"instruction count". The client's `evaluate()` in `handle.go` already calls
`UnmarshalProgram` which handles the MZBC header.

**Option B**: Keep `ProgramLen` as instruction count but also store the full
blob. Add a `ProgramBlobLen uint16` field to `FlatAttrNode` (taking space from
`_pad2`).

Option A is simpler and backward-compatible if we accept that `ProgramLen` now
means byte length (divided by some unit) or is repurposed.

**Decision needed**: Which option? Option A requires changing how `evaluate()`
calculates the data slice bounds. Currently it does:
```go
totalBytes := int(bcLen) * 16
data := sharedPR.Bytecode[bcOff : uint32(bcOff)+uint32(totalBytes)]
```
This would need to change if ProgramLen stores byte count.

### 2. Write `handletest` priest

Create `flock/cmd/handletest/main.go` — a new priest that exercises the
`Handle[T]` API end-to-end through actual kernel syscalls.

Test cases (mirroring the existing attrtest examples but using Handle[T]):

**Test 1: Value handles round-trip**
- Create `ValueI64("attr:///priest/handletest/foo", 42)`
- Verify `Get() == 42`
- `Set(99)`, verify `Get() == 99`

**Test 2: String handles**
- Create `ValueStr("attr:///priest/handletest/name", "hello")`
- Verify `Get() == "hello"`
- `Set("world")`, verify `Get() == "world"`

**Test 3: Constraint evaluation**
- Create two value handles (foo, bar)
- Create a constraint handle with a hand-assembled `vm.Program` that adds them
- Set foo=10, bar=20
- Verify constraint `Get() == 30`
- Set foo=5, verify constraint `Get() == 25` (lazy re-evaluation)

**Test 4: Shared-page trie lookup**
- Create a value handle
- Use `Exists("attr:///priest/handletest")` to verify trie presence
- Use `Find("attr:///priest/handletest/*")` (may return nil until collection
  writing is implemented, but should not crash)

**Test 5: Multi-type handles**
- Create `ValueBool`, `ValueF64`, `ValueTribool` handles
- Verify Get/Set round-trips for each type

### 3. Add `handletest` to TOML configs and Taskfile

- Add `handletest` priest to all 3 TOML configs
- Add build task in `Taskfile.yml` (copy from `attrtest` pattern)
- Add to FAT32 image staging

### 4. Fix `evaluate()` bytecode loading

The `evaluate()` method in `handle.go` currently calculates bytecode slice
bounds using `ProgramLen * 16` (legacy instruction count). After fixing
`SysAttrCreate` (step 1), this needs to match the new storage format.

Additionally, `evaluate()` needs to handle the case where the bytecode in the
shared page was stored as the full serialized Program (MZBC format) vs legacy
raw instructions. The current fallback logic handles this:
```go
prog, err := vm.UnmarshalProgram(data)
if err != nil {
    prog = unmarshalLegacyInstructions(data, int(bcLen))
}
```
This is correct for backward compatibility, but the slice bounds must be right.

### 5. Verify cross-priest attribute visibility (optional, stretch)

Two priests creating attributes under different URI prefixes should be able to
see each other's values via the shared-page trie. This requires:
- Priest A creates `attr:///priest/priestA/x`
- Priest B does `trieLookupShared("attr:///priest/priestA/x")` and reads the
  CachedValue via seqlock

This works because both priests map the same physical pages read-only. The trie
walk and seqlock read don't require syscalls.

## File Summary

### New files
| File | Purpose |
|------|---------|
| `flock/cmd/handletest/main.go` | Integration test priest for Handle[T] API |

### Modified files
| File | Change |
|------|--------|
| `kmazarin/ksyscall/constraint_syscall.go` | Update SyscallAttrCreate bytecode storage for full Program blobs |
| `mazarin/attr/handle.go` | Fix evaluate() bytecode slice bounds |
| `mazarin/vm/flat/node.go` | Possibly update ProgramLen semantics |
| `config/kmazarin-arm64.toml` | Add handletest priest |
| `config/kmazarin-riscv64.toml` | Add handletest priest |
| `config/kmazarin-x86_64.toml` | Add handletest priest |
| `Taskfile.yml` | Add handletest build task |

## Verification

1. `go test mazzy/mazarin/vm/...` — marshal round-trip still works
2. `go test mazzy/mazarin/attr/...` — existing in-process tests still pass
3. Cross-compile all 3 architectures
4. Run ARM64 HVF with handletest priest — verify all test cases pass
5. Run ARM64 TCG, x86_64, RISC-V — 120s stability with handletest
6. Existing attrtest priest still passes alongside handletest
