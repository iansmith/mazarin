# Constraint VM Phase 5C — Runtime Validation & Cross-Priest Visibility

## Context

Phase 5B completed the build infrastructure and ProgramLen bug fix. All code
compiles and disk images build successfully on all 3 architectures (ARM64,
x86_64, RISC-V). **However, handletest has NOT been run at runtime yet.**

## What Was Done (Phase 5B)

### ProgramLen bug fix (3 files)
- `kmazarin/ksyscall/constraint_syscall.go:107` — Changed from
  `ProgramLen = uint16(bytecodeLen / 16)` to `ProgramLen = uint16(bytecodeLen)`.
  The old code treated the MZBC blob as raw 16-byte instructions, truncating the
  string table and func table via integer division.
- `mazarin/attr/handle.go:128-129` — Changed from `totalBytes = int(bcLen) * 16`
  to `totalBytes = int(bcLen)`. Matches the new ProgramLen-as-byte-count
  semantics.
- `mazarin/vm/flat/node.go:35` — Updated `ProgramLen` comment from "number of
  instructions" to "byte length of serialized program (MZBC blob)".

### New convenience constructor
- `mazarin/vm/inst.go` — Added `InstConstStr(index uint16) Inst` to match
  existing `InstConstI64`, `InstConstF64`, `InstConstBool` pattern.

### handletest priest (`flock/cmd/handletest/main.go`)
6 test cases exercising Handle[T] through kernel syscalls:
1. **ValueI64 round-trip** — Create, Get(42), Set(99), Get(99)
2. **ValueStr round-trip** — Create "hello", Get, Set "world", Get
3. **Multi-type** — Bool, F64, Tribool value handles with Get/Set
4. **Constraint add** — Hand-assembled VM program: `deref(a) + deref(b)` with
   lazy re-evaluation after Set(). Uses BuiltinDerefI64 + OpAdd + string table.
5. **Constraint chain** — `a,b → sum(a+b) → doubled(sum*2)` with dirty
   propagation across two constraint levels.
6. **Trie Exists()** — Shared-page trie walk for prefix existence check.

### Build integration
- 3 Taskfile variables: `HANDLETEST_ELF`, `HANDLETEST_AMD64_ELF`,
  `HANDLETEST_RISCV64_ELF`
- 3 build tasks: `handletest`, `handletest-x86_64`, `handletest-riscv64`
- All 3 disk image tasks updated (deps, sources, staging copy, mkfat32 args)
- All 3 TOML configs have `[[priest]] name = "handletest" path = "/handletest.elf"`

### Build verification
- `$GO tool task disk-arm64` — success (handletest.elf = 2,671,847 bytes)
- `$GO tool task disk-x86_64` — success (handletest.elf = 2,746,149 bytes)
- `$GO tool task disk-riscv64` — success (handletest.elf = 2,680,398 bytes)
- `$GO test mazzy/mazarin/vm/...` — pass
- `$GO test mazzy/mazarin/attr/...` — pass

## What Needs To Happen (Phase 5C)

### 1. Runtime test on ARM64 HVF

Run the ARM64 build with handletest and verify all 6 tests pass:
```bash
$GO tool task run TIMEOUT=30
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

Look for:
```
handletest: All tests PASSED.
```

**Likely failure modes:**
- `attr.Init()` panic — SharedPageHeader not mapped at `0x00007FFD00000000`.
  This would mean the kernel's constraint page mapping to userspace isn't set up
  for handletest. Check that the kernel maps constraint pages into every priest
  (not just specific ones). See `kmazarin/ksyscall/constraint_mgr.go` for the
  `mapConstraintPagesForPriest()` call — it should happen during priest launch
  for all priests, not just attrtest.
- `AttrCreate` returns EPERM — handletest priest's PID doesn't match the
  ownership check in `SyscallAttrCreate`. The URI prefix
  `attr:///priest/handletest/...` must match the priest's registered name.
  Check how the kernel maps priest names to PIDs in the TOML launch path.
- Constraint evaluation panic — bytecode deserialization fails. Since we fixed
  ProgramLen, this should work, but verify the MZBC magic bytes survive the
  kernel bytecode copy correctly.
- Seqlock spin — if the kernel doesn't bump SeqCounter correctly on writes,
  the client-side seqlock read in `Get()` will spin forever. Would manifest
  as handletest hanging (no output after a certain test).

### 2. Runtime test on ARM64 TCG, x86_64, RISC-V

After ARM64 HVF passes:
```bash
$GO tool task run TIMEOUT=30                    # ARM64 TCG (no HVF)
$GO tool task run-x86_64 TIMEOUT=30
$GO tool task run-riscv64 TIMEOUT=30
```

Check serial logs for all platforms. The attrtest priest should still pass
alongside handletest.

### 3. Stability test (120s)

Once all 4 platforms show "All tests PASSED", run longer stability:
```bash
$GO tool task run TIMEOUT=120
```

Handletest should print its results and then exit (the priest terminates after
`main()` returns). Verify no crashes or hangs after handletest completes — the
other priests (dapope, stdio, sievetest, attrtest) must continue running stably
for the full 120s.

### 4. Cross-priest attribute visibility (stretch goal)

If all runtime tests pass, verify that attrtest's in-process attributes and
handletest's kernel-managed attributes coexist in the same shared pages. This
should work automatically since both priests map the same physical constraint
pages read-only. Verify by checking that `Exists("attr:///priest/attrtest")`
returns false from handletest (attrtest uses the in-process API, NOT the kernel
Handle[T] API — its attributes are NOT in the shared namespace).

If cross-priest visibility is desired for a future test, a second priest would
need to also use the Handle[T] API and look up the first priest's URIs via
`trieLookupShared()`.

### 5. Investigate and fix any runtime failures

Based on failure mode, fixes may be needed in:
- `kmazarin/ksyscall/constraint_mgr.go` — constraint page mapping for all priests
- `kmazarin/ksyscall/constraint_trie.go` — trie insert correctness
- `kmazarin/ksyscall/launch.go` — priest name → PID registration
- `mazarin/attr/handle_resolve.go` — sharedResolver trie walking
- `mazarin/attr/handle.go` — evaluate() error handling

## File Summary

No new files expected. Modifications depend on runtime failure analysis.

### Files to inspect if failures occur
| File | What to check |
|------|---------------|
| `kmazarin/ksyscall/constraint_mgr.go` | `mapConstraintPagesForPriest()` — called for all priests? |
| `kmazarin/ksyscall/launch.go` | Priest name registration for ownership checks |
| `kmazarin/ksyscall/constraint_trie.go` | Trie insert + seqlock correctness |
| `mazarin/attr/handle_init.go` | SharedPageHeader VA correct? |
| `mazarin/attr/handle.go` | evaluate() bytecode slice bounds |
| `mazarin/attr/handle_resolve.go` | sharedResolver.Deref trie walk |

## Verification

1. ARM64 HVF: `handletest: All tests PASSED.` in serial log
2. ARM64 TCG: same
3. x86_64: same
4. RISC-V: same
5. 120s stability on at least ARM64 — no crashes after handletest exits
6. attrtest still passes alongside handletest on all platforms
