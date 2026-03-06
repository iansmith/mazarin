# Continuation Prompt: Implement .mzr Modules + TOML Boot Sequence

## Context

You are implementing the plan in `design/MZR-AND-BOOT-SEQUENCE.md`. Read that
file first — it is the authoritative design document. This prompt gives you
the precise file locations, line numbers, and implementation order.

The goal: replace the hardcoded priest launch sequence in the kernel with a
TOML-driven boot config, and add support for .mzr (fixed-address, non-PIE)
modules that work on all architectures including RISC-V.

## Phase 1: .mzr support in the kernel loader

### 1a. Create `shared/constants/mzr_slots.go`

Define slot address constants. Slots are 32MB apart starting at 0x30000000:

```go
const (
    MzrSlot0 = 0x30000000 // fs
    MzrSlot1 = 0x32000000 // reserved
    MzrSlot2 = 0x34000000 // reserved
    MzrSlot3 = 0x36000000 // reserved
    MzrSlotSpacing = 0x02000000 // 32MB between slots
)
```

### 1b. Modify `kmazarin/ksyscall/loadmaz.go` — add ET_EXEC path

The current code at line 156-160 rejects non-PIE binaries:

```go
// Verify it's a PIE (ET_DYN)
if hdr.Type != 3 { // ET_DYN = 3
    console.KWriteString("[LoadMaz] ERROR: not a PIE binary (expected ET_DYN)\r\n")
    return int64(errInvalidELF)
}
```

Change this to a type switch:
- `ET_DYN (3)` → existing PIE path (unchanged)
- `ET_EXEC (2)` → new fixed-address path

For ET_EXEC, the key differences are:
- `loadBase` = lowest LOAD segment `p_vaddr` from the ELF (NOT `priest.HighestVA + gap`)
- `loadOffset` = 0 (binary runs at its linked address)
- Skip `applyPIERelocations` (line 223) — there are no `.rela.dyn` entries
- `entryPoint` = symbol address directly (no `+ loadOffset`)
- `moduledataVA` = symbol address directly (no `+ loadOffset`)
- Still call `resolveMazImports` (line 226) — thin stub patching works the same
- Still update `priest.HighestVA` (lines 258-263)

The segment loading loop (lines 201-219) needs adjustment for ET_EXEC:
- Don't add `loadOffset` to `adjustedPhdr.Vaddr` / `adjustedPhdr.Paddr`
- The phdr addresses ARE the target addresses

### 1c. Verify `cmd/maz-reloc/main.go` handles ET_EXEC

It already does — `debug/elf.Open` handles both types transparently, and
`maz-reloc` never checks `f.Type`. No changes needed. Confirm by reading
the file and verifying there's no ET_DYN/ET_EXEC check.

### 1d. Add fs-mzr build tasks to `Taskfile.yml`

Add a variable like `FS_MZR_RISCV64: build/fs-riscv64.mzr` and a task:

```yaml
fs-mzr-riscv64:
  desc: Build fs.mzr for RISC-V 64-bit (fixed-address slot 0)
  deps: [check-env, maz-overlay]
  cmds:
    - 'CGO_ENABLED=0 GOARCH=riscv64 GOOS=linux {{.GO}} build
       -overlay={{.MAZ_OVERLAY_JSON}}
       -ldflags="-T 0x30000000"
       -o {{.FS_MZR_RISCV64}} ./flock/cmd/fat32'
    - '{{.GO}} tool maz-reloc {{.FS_MZR_RISCV64}} {{.THIN_MANIFEST}}'
```

Note: NO `-buildmode=pie`. The `-T 0x30000000` flag places text at that address.
Remember Go's `-T` behavior: first LOAD segment will be at `0x30000000 - 0x10000`
(64KB before requested address). The loader must handle this.

Update `disk-riscv64` task to depend on `fs-mzr-riscv64` and include the .mzr
in the disk image (same way fs.maz is included for ARM64).

### 1e. Test Phase 1

```bash
$GO tool task run-riscv64 TIMEOUT=15
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

Look for: `[LoadMaz] base=0x2fff0000` (or 0x30000000), `offset=0x0`, successful
entry point resolution, and the disk priest entering its FAT32 serve loop.

---

## Phase 2: TOML boot sequence

### 2a. Create `shared/constants/boot_config.go`

Copy the structs from the design doc. Key types:
- `BootModule` — `Name [MaxNameLen]byte` + `Path [MaxPathLen]byte`
- `BootPriest` — `Name`, `Path`, `Mzr[8]BootModule`, `Maz[8]BootModule`, counts
- `BootConfig` — hardware constraints, `Timezone`, `BootstrapPriests[4]`, `Priests[8]`, counts

All fixed-size arrays, zero allocation. Include a `NullTermString(b []byte) string`
helper that finds the first zero byte.

### 2b. Create `shared/toml/parser.go`

Minimal hand-written TOML parser. Input: `[]byte`. Output: `*constants.BootConfig`.

Must handle:
- Comment lines (`#`)
- `key = 123` (integer values)
- `key = "string"` (quoted strings)
- `key = ["name1=/path1", "name2=/path2"]` (string arrays, single line)
- `[[bootstrap_priest]]` (starts a new bootstrap priest entry)
- `[[priest]]` (starts a new application priest entry)

String array entries with `=` are split on the first `=` to get name and path.

The parser is ~200 lines. It does NOT need to handle:
- Multi-line arrays
- Nested tables beyond `[[tablename]]`
- Escape sequences in strings
- Bare keys with dots

The existing diplomat parser (`diplomat/main/config.go` lines 112-188) is a
good reference for the line-by-line scanning pattern, but the new parser is
in `shared/` so both kernel and fs module can import it.

**IMPORTANT**: The kernel has a full Go runtime, so this parser CAN use
strings, slices, etc. It does NOT need the diplomat's bare-metal constraints.
However, the output struct (`BootConfig`) is fixed-size arrays for simplicity.

### 2c. Modify `kmazarin/kmazarin/main.go` — replace hardcoded launches

The hardcoded launches are at lines 886-917:

```go
// Launch disk priest
diskName := "/disk.elf\x00"
...
result := ksyscall.SyscallLaunch(uint64(diskPtr), 0, 0, 0, 0, 0)
...
// Launch dapope
dapopeName := "/dapope.elf\x00"
...
// Launch stdio
stdioName := "/stdio.elf\x00"
```

Replace with:
1. Read `/kmazarin.toml` from FAT32 using the kernel's built-in reader
2. Parse with `shared/toml` parser
3. Loop over `cfg.BootstrapPriests` only
4. For each: launch priest, load its modules, sync, print status
5. Fall back to hardcoded `disk.elf` if no TOML file or no bootstrap priests

The kernel currently reads FAT32 via `device.GetBlockDevice()` + `fat32.NewFileSystem()`.
The TOML file is at the root directory: `/kmazarin.toml`.

### 2d. Create `/kmazarin.toml` in disk images

Update disk image build tasks in `Taskfile.yml` to include a `kmazarin.toml` file.
Per-architecture content differs (RISC-V uses .mzr, ARM64 uses .maz for fs):

**RISC-V kmazarin.toml:**
```toml
timezone = "America/New_York"
[[bootstrap_priest]]
name = "disk"
path = "/disk.elf"
mzr = ["fs=/fs.mzr"]
[[priest]]
name = "dapope"
path = "/dapope.elf"
[[priest]]
name = "stdio"
path = "/stdio.elf"
```

**ARM64 kmazarin.toml:**
```toml
timezone = "America/New_York"
[[bootstrap_priest]]
name = "disk"
path = "/disk.elf"
maz = ["fs=/fs.maz"]
[[priest]]
name = "dapope"
path = "/dapope.elf"
[[priest]]
name = "stdio"
path = "/stdio.elf"
```

### 2e. Test Phase 2

Run on all architectures. The boot log should show `[boot] disk: loaded fs`
instead of the old hardcoded launch messages. Verify dapope and stdio still
launch (they're `[[priest]]` entries but for now the kernel can launch them
too — Phase 4 moves them to fs).

---

## Phase 3: Kernel-loads-modules-before-start

This phase changes the launch-then-load flow to load-then-start.

### Current flow (priest loads its own modules):
1. Kernel launches priest → priest's `main()` runs
2. Priest calls `sys.LoadMaz("/fs.maz")` → syscall to kernel
3. Kernel loads module into priest's address space
4. Priest calls module entry point

### New flow (kernel pre-loads modules):
1. Kernel creates priest (but doesn't start it)
2. Kernel loads modules into priest's page table
3. Kernel starts priest → priest's `main()` runs with modules already mapped

This requires:
- A way to create a priest without starting it (or a `KernelLoadModule` that
  works on a not-yet-running priest)
- Modifying `flock/cmd/disk/main.go` to NOT call `sys.LoadMaz` itself —
  the module is already there

### 3a. Modify the boot loop

Currently `SyscallLaunch` both creates and starts the priest. Either:
- Add a new internal kernel function `KernelLoadModule(priestPID, path)` that
  loads a module into a priest that exists but hasn't started yet, OR
- Load modules between `SyscallLaunch` and the priest actually getting scheduled
  (if there's a window — check how priest scheduling works)

### 3b. Modify `flock/cmd/disk/main.go`

Remove the `sys.LoadMaz("/fs.maz")` call. The priest's `main()` should assume
fs is already mapped. The entry point calling pattern changes.

---

## Phase 4: fs launches application priests (FUTURE)

This phase is documented in the design but can be deferred. It requires:
- New syscalls: `SysLaunchPriest` (0x101C?), `SysLoadModuleIntoPriest` (0x101D?),
  `SysStartPriest` (0x101E?)
- fs.mzr reading `/kmazarin.toml` from its own filesystem
- fs.mzr calling `SysLaunchPriest` for each `[[priest]]` entry
- `SysLoadMaz` delegation registration (infrastructure already exists in
  `kmazarin/ksyscall/delegate.go` and `mazarin/sys/delegate.go`)

For now, the kernel can launch ALL priests (both bootstrap and application) from
the TOML file. Phase 4 moves the `[[priest]]` launches to fs.

---

## Key files to read before starting

Read these files to understand the current code before modifying:

1. `design/MZR-AND-BOOT-SEQUENCE.md` — the full design document
2. `kmazarin/ksyscall/loadmaz.go` — current .maz loader (lines 130-293 are DoLoadMazWork)
3. `kmazarin/kmazarin/main.go` — lines 880-920 for hardcoded priest launches
4. `cmd/maz-reloc/main.go` — maz-reloc tool (confirm no ET_DYN check)
5. `diplomat/main/config.go` — existing TOML parser (reference for scanning pattern)
6. `mazarin/sys/syscall.go` — syscall number table (highest is 0x101B)
7. `Taskfile.yml` — build tasks (look for `fs-maz-*` and `disk-*` tasks)
8. `flock/cmd/disk/main.go` — disk priest (calls sys.LoadMaz)
9. `flock/cmd/fat32/main.go` — fs module code
10. `shared/constants/` — existing constant files for naming conventions

## Implementation order

**Do Phase 1 first, test it, then Phase 2, test it.** Each phase should
produce a working system. Don't try to do all phases at once.

Phase 1 is the smallest and most impactful — it unblocks .mzr on RISC-V.
Phase 2 is the biggest code change (new parser + boot loop rewrite).
Phase 3 is a refinement (changes the load-start ordering).
Phase 4 is future work (moves application priest launches to userspace).
