# .mzr Modules and TOML Boot Sequence

## Problem

.maz files are PIE binaries. Go does not support internal PIE linking for
linux/riscv64, and no Linux RISC-V cross-compiler is installed. We need
a module format that works on all three architectures without PIE.

Additionally, the boot sequence is hardcoded in `kmazarin/kmazarin/main.go`
(lines 886-917). Adding or reordering priests requires recompiling the kernel.

## Design

### Two module formats

**.maz** — PIE (`ET_DYN`). Position-independent. Loaded at any address. Requires
`.rela.dyn` relocations. Works on arm64 and amd64. Not supported on riscv64
until Go adds internal PIE linking for that target.

**.mzr** — Fixed-address (`ET_EXEC`). Built with `-T <slot_address>`. No PIE
relocations needed — the binary runs at its linked address. Works on all
architectures. Each .mzr gets a dedicated address slot assigned at build time.

The kernel loader (`SysLoadMaz` / `SysLoadMzr`) checks `e_type` in the ELF
header: `ET_DYN` (3) → PIE relocation path. `ET_EXEC` (2) → fixed-address path.
A single syscall handles both; the kernel auto-detects.

### .mzr address slots

Each .mzr is built with a specific `-T` address. Slots are spaced 32MB apart
starting at 0x30000000, well above the host priest's text/data/heap and well
below the stack.

| Slot | Address      | Purpose                |
|------|-------------|------------------------|
| 0    | 0x30000000  | fs (filesystem)        |
| 1    | 0x32000000  | reserved               |
| 2    | 0x34000000  | reserved               |
| 3    | 0x36000000  | reserved               |
| ...  | +0x02000000 | future modules         |

The slot table lives in `shared/constants/mzr_slots.go` so that both the
Taskfile and the kernel can reference it. The Taskfile reads the slot address
for each .mzr from this table (via a build tool or hardcoded constant).

### .mzr kernel loading path

`DoLoadMazWork` in `kmazarin/ksyscall/loadmaz.go` changes:

```
Current (PIE only):
  1. Read ELF from disk
  2. Verify ET_DYN
  3. Allocate VA range above priest.HighestVA
  4. Copy LOAD segments at allocated VA
  5. Apply .rela.dyn RELATIVE relocations
  6. Resolve .maz_imports (thin stub patches)
  7. Register moduledata (adjust by delta)
  8. Return entry point (adjusted by delta)

New (PIE + fixed-address):
  1. Read ELF from disk
  2. Check e_type:
     ET_DYN (3) → existing PIE path (steps 3-8 unchanged)
     ET_EXEC (2) → fixed-address path:
       3. Read LOAD segment p_vaddr values — these are the target addresses
       4. Map pages at exactly those VAs in priest's page table
       5. Copy LOAD segments to their linked addresses
       6. Skip .rela.dyn (none exists, all pointers correct)
       7. Resolve .maz_imports (thin stub patches — still needed)
       8. Register moduledata (delta = 0, adjustments are no-ops)
       9. Return e_entry directly (no adjustment)
```

Step 4 is the key change: instead of `loadBase = priest.HighestVA + gap`,
use `loadBase = phdr.Vaddr` from the ELF. If any page in that range is
already mapped, the load fails with a clear error (address conflict).

### .mzr build changes

The `maz-reloc` tool currently requires `ET_DYN`. It needs to also accept
`ET_EXEC` for .mzr files. The thin stub import table (`.maz_imports` /
`.maz_import_strtab`) is generated the same way for both formats — it
patches BL/CALL instructions at known offsets regardless of PIE status.

Taskfile additions for each .mzr:

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

Note: no `-buildmode=pie`. This is a regular static Go binary placed at a
specific address. The `-T` flag controls where the linker places the text
segment.

---

## TOML Boot Sequence

### Config file

The boot sequence is defined in `/kmazarin.toml` on the FAT32 disk (root
directory, not in /EFI/Linux/). The kernel reads this file using its
built-in FAT32 reader during early boot, before any priests are launched.

```toml
# kmazarin.toml — boot sequence configuration

# Hardware constraints (existing, unchanged)
min_cpus = 1
max_cpus = 4
min_ram_mb = 512
max_ram_mb = 8192
kernel_budget_mb = 128

# Timezone for the system clock. Uses IANA timezone names as understood
# by Go's time.LoadLocation(). Examples:
#   "America/New_York"      — US Eastern
#   "America/Chicago"       — US Central
#   "America/Denver"        — US Mountain
#   "America/Los_Angeles"   — US Pacific
#   "Europe/London"         — UK
#   "UTC"                   — UTC
timezone = "America/New_York"

# Bootstrap priests: loaded by the kernel using its built-in FAT32 reader
# during early boot, BEFORE the filesystem is available. These are the
# minimum priests needed to get the filesystem running. The kernel parses
# only [[bootstrap_priest]] sections; it ignores [[priest]] sections.
#
# Each section specifies:
#   name  — short identifier for IPC addressing
#   path  — filesystem path to the priest ELF
#   mzr   — list of .mzr modules: "name=/path" (optional)
#   maz   — list of .maz modules: "name=/path" (optional)
#
# Module entries use "name=/path" format. The name is a short identifier
# used for IPC routing (e.g., "fs"). The path is the filesystem path to
# the module binary. Split on the first '=' to parse.

[[bootstrap_priest]]
name = "disk"
path = "/disk.elf"
mzr = ["fs=/fs.mzr"]

# Application priests: loaded by the fs module after the filesystem is
# running. The fs module reads this file from disk, parses the [[priest]]
# sections, and launches each priest via SysLaunchPriest. The kernel
# never sees these sections.
#
# Same format as bootstrap_priest.

[[priest]]
name = "dapope"
path = "/dapope.elf"
maz = ["keyboard=/keyboard.maz", "mouse=/mouse.maz", "clock=/clock.maz"]

[[priest]]
name = "stdio"
path = "/stdio.elf"
```

### TOML parser changes

The existing parser in `diplomat/main/config.go` handles simple `key = value`
pairs. The kmazarin kernel needs its own parser (or a shared one) that also
handles:

- `[[bootstrap_priest]]` — TOML array-of-tables. Kernel-loaded priests (parsed by kernel).
- `[[priest]]` — TOML array-of-tables. Application priests (parsed by fs module).
- `name = "disk"` — string values (priest and module identifiers).
- `path = "/disk.elf"` — string values (currently only integers are parsed).
- `mzr = ["fs=/fs.mzr"]` — string arrays with `name=path` entries.
- `maz = ["keyboard=/keyboard.maz"]` — string arrays with `name=path` entries.
- `timezone = "America/New_York"` — string value (IANA timezone name).

The parser does NOT need to be a full TOML implementation. It needs:
- Comment lines (`#`)
- Simple `key = value` (integers, for hardware config)
- Simple `key = "string"` (quoted strings)
- Simple `key = ["a", "b"]` (string arrays, single line)
- `[[tablename]]` (array-of-tables, to delimit priest entries)

String array entries that contain `=` are split on the first `=` to
extract `name` and `path` (e.g., `"fs=/fs.mzr"` → name=`fs`, path=`/fs.mzr`).

This is a ~200 line hand-written parser with no allocations (pre-allocated
fixed-size buffers).

### Kernel data structures

```go
// shared/constants/boot_sequence.go

const (
    MaxBootstrapPriests = 4   // Maximum bootstrap priests (kernel-loaded)
    MaxPriests          = 8   // Maximum application priests (fs-loaded)
    MaxModulesPerPriest = 8   // Maximum .maz/.mzr per priest
    MaxPathLen          = 64  // Maximum path string length
    MaxNameLen          = 32  // Maximum name identifier length
    MaxTimezoneLen      = 48  // Maximum timezone string length (e.g., "America/New_York")
)

// BootModule describes a named module (.maz or .mzr) to load.
type BootModule struct {
    Name [MaxNameLen]byte // Module name for IPC (null-terminated)
    Path [MaxPathLen]byte // Filesystem path (null-terminated)
}

// BootPriest describes one priest in the boot sequence.
type BootPriest struct {
    Name     [MaxNameLen]byte            // Priest name for IPC (null-terminated)
    Path     [MaxPathLen]byte            // Priest ELF path (null-terminated)
    Mzr      [MaxModulesPerPriest]BootModule // .mzr modules
    Maz      [MaxModulesPerPriest]BootModule // .maz modules
    MzrCount int
    MazCount int
}

// BootConfig is the parsed /kmazarin.toml configuration.
// The kernel parses the full file but only acts on BootstrapPriests.
// The fs module re-parses the file and acts on Priests.
type BootConfig struct {
    // Hardware constraints (existing)
    MinCpus       int
    MaxCpus       int
    MinRamMB      int
    MaxRamMB      int
    KernelBudgetMB int

    // Timezone
    Timezone [MaxTimezoneLen]byte // IANA timezone (null-terminated)

    // Bootstrap priests — loaded by kernel using built-in FAT32 reader
    BootstrapPriests     [MaxBootstrapPriests]BootPriest
    BootstrapPriestCount int

    // Application priests — loaded by fs module after filesystem is up
    Priests     [MaxPriests]BootPriest
    PriestCount int
}
```

All fixed-size, zero-allocation. Parsed from the TOML file during early
kernel init, before any priests launch.

### Kernel boot loop (bootstrap priests only)

Replace the hardcoded priest launches in `main.go` with:

```go
// Parse boot config from /kmazarin.toml
cfg := parseBootConfig(fat32FS)

// Only launch bootstrap priests — the kernel's built-in FAT32 reader
// handles all file I/O at this stage.
for i := 0; i < cfg.BootstrapPriestCount; i++ {
    p := &cfg.BootstrapPriests[i]
    priestName := nullTermString(p.Name[:])
    priestPath := nullTermString(p.Path[:])

    // Launch the priest ELF
    result := ksyscall.SyscallLaunch(priestPath, ...)
    if result != 0 {
        console.KPrintf("[boot] FATAL: %s launch failed\n", priestPath)
        continue
    }

    // Load .mzr modules into the priest
    for j := 0; j < p.MzrCount; j++ {
        modName := nullTermString(p.Mzr[j].Name[:])
        modPath := nullTermString(p.Mzr[j].Path[:])
        ksyscall.KernelLoadModule(priestPID, modPath)
        console.KPrintf("[boot] %s: loaded %s\n", priestName, modName)
    }

    // Load .maz modules into the priest
    for j := 0; j < p.MazCount; j++ {
        modName := nullTermString(p.Maz[j].Name[:])
        modPath := nullTermString(p.Maz[j].Path[:])
        ksyscall.KernelLoadModule(priestPID, modPath)
        console.KPrintf("[boot] %s: loaded %s\n", priestName, modName)
    }

    kmem.FinalUserspaceSync()
    console.KPrintf("[boot] %s ready\n", priestName)
}
// At this point, disk+fs are running. The kernel's job is done.
// The fs module reads /kmazarin.toml itself and launches [[priest]]
// entries via SysLaunchPriest.
```

**Key detail**: The kernel loads ALL modules (both .mzr and .maz) for a
bootstrap priest BEFORE starting the priest's main thread. The priest's
`main()` runs after all its modules are already mapped into its address
space. This means the priest can call into its modules immediately
without waiting for async loads.

### Application priest launch (fs module)

After the disk priest starts and fs.mzr mounts FAT32, the fs module
takes over launching the remaining priests:

```go
// Inside fs.mzr, after FAT32 is mounted and serving

// Re-read and parse /kmazarin.toml from disk
data := readFile("/kmazarin.toml")
cfg := parseBootConfig(data)

// Launch application priests (the [[priest]] sections)
for i := 0; i < cfg.PriestCount; i++ {
    p := &cfg.Priests[i]
    priestName := nullTermString(p.Name[:])
    priestPath := nullTermString(p.Path[:])

    // Ask the kernel to launch the priest
    pid := sys.LaunchPriest(priestPath)

    // Load modules into the new priest via SysLoadMaz
    // (which is now handled by fs itself via delegation)
    for j := 0; j < p.MzrCount; j++ {
        sys.LoadModuleIntoPriest(pid, p.Mzr[j].Path[:])
    }
    for j := 0; j < p.MazCount; j++ {
        sys.LoadModuleIntoPriest(pid, p.Maz[j].Path[:])
    }

    sys.StartPriest(pid)
}
```

**The TOML parser is shared code** in `shared/` — both the kernel and
the fs module import the same parser. The kernel uses it during early
boot (reading raw bytes from its built-in FAT32 reader). The fs module
uses it later (reading from its own filesystem). Same parser, different
I/O sources.

### Syscall delegation handoff

After the disk priest starts and fs.mzr mounts FAT32, fs.mzr registers
as the handler for `SysLoadMaz` and also reads `/kmazarin.toml` to
launch the application priests:

```go
// Inside fs.mzr, called by disk.elf after its main() starts
func Start() {
    mountFAT32()

    // Register as the filesystem — all future SysLoadMaz calls
    // are forwarded to us via delegation.
    sys.RegisterSyscallHandler(sys.SysLoadMaz, handleLoadMaz)

    // Now launch application priests from the TOML config.
    // We own the filesystem, so we read the file ourselves.
    launchApplicationPriests()

    // Enter serve loop for ongoing SysLoadMaz requests.
    serveLoop()
}

func handleLoadMaz(req *sys.SyscallRequest) {
    filename := req.StringArg(0)
    data, err := readFile(filename) // FAT32 read via block device
    if err != nil {
        req.Reply(-1, nil)
        return
    }
    // Hand file data to kernel for ELF parsing + mapping
    req.ReplyWithPages(data)
}
```

From this point, any priest calling `sys.LoadMaz(path)` has its request
forwarded to the disk priest. The kernel's built-in FAT32 reader is no
longer used.

This is identical to how `write()` is handled: the kernel has a default
implementation (serial UART), and the stdio priest registers to override it.
The delegation table is the switching mechanism.

---

## Implementation phases

### Phase 1: .mzr support in the kernel loader

1. Modify `DoLoadMazWork` to handle `ET_EXEC` (fixed-address) in addition
   to `ET_DYN` (PIE). The check is a single `if hdr.Type == 2` branch.
2. For `ET_EXEC`: set `loadOffset = 0`, map at ELF's own VAs, skip
   `.rela.dyn`, resolve `.maz_imports` as before.
3. Update `maz-reloc` tool to accept `ET_EXEC` binaries.
4. Add `shared/constants/mzr_slots.go` with slot address constants.
5. Add `fs-mzr-riscv64` task to Taskfile.
6. Include `fs.mzr` in the RISC-V disk image.
7. Test: `disk.elf` loads `fs.mzr`, mounts FAT32, enters serve loop on RISC-V.

**Deliverable**: fs runs on RISC-V via .mzr. The ARM64 path continues
using .maz (PIE) as before.

### Phase 2: TOML boot sequence (bootstrap priests)

1. Add TOML parser to `shared/` that handles `[[bootstrap_priest]]`,
   `[[priest]]`, string values, and string arrays with `name=path` entries.
2. Add `BootConfig` / `BootPriest` / `BootModule` structs to `shared/constants/`.
3. Replace hardcoded priest launches in `main.go` with the bootstrap loop
   (only `[[bootstrap_priest]]` entries).
4. Read `/kmazarin.toml` from FAT32 during early init.
5. If no TOML file exists, fall back to a hardcoded default sequence
   (disk.elf + fs) so existing disk images work.
6. Create `/kmazarin.toml` on each arch's disk image.

**Deliverable**: Bootstrap sequence is data-driven. Adding a bootstrap
priest is a TOML edit, not a kernel recompile.

### Phase 3: Kernel-loads-modules-before-start

1. Add `KernelLoadModule(priestPID, path)` — kernel loads a .maz/.mzr
   into a priest that has been created but not yet started.
2. The priest's `main()` sees all modules already mapped and can call
   into them immediately.
3. The priest code changes from:
   ```go
   // OLD (priest loads its own module)
   func main() {
       result := sys.LoadMaz("/fs.mzr")
       callEntry(result.EntryPoint)
   }
   ```
   to:
   ```go
   // NEW (modules pre-loaded by kernel, priest just uses them)
   func main() {
       // fs.mzr is already mapped at 0x30000000
       // its init function was already called
       fsServe()
   }
   ```
   The priest doesn't need to know how to load modules — the kernel did
   it based on the TOML config.

### Phase 4: fs launches application priests

1. After the disk priest starts and fs.mzr mounts FAT32, fs.mzr registers
   as the `SysLoadMaz` handler via syscall delegation.
2. fs.mzr reads `/kmazarin.toml` from disk, parses `[[priest]]` sections.
3. For each application priest, fs.mzr calls `SysLaunchPriest(path)` to
   create the priest, then loads its modules via `SysLoadModuleIntoPriest`,
   then starts it via `SysStartPriest`.
4. The TOML parser is shared code (`shared/`) — same parser used by kernel
   (Phase 2) and fs module (Phase 4), just different I/O sources.
5. The kernel's built-in FAT32 reader becomes dormant after bootstrap.

**Deliverable**: Full microkernel boot path. Kernel bootstraps disk+fs,
then fs takes over launching everything else from the config file.

---

## Files to create or modify

| File | Action | Purpose |
|------|--------|---------|
| `shared/constants/mzr_slots.go` | Create | .mzr slot address constants |
| `shared/constants/boot_config.go` | Create | BootConfig/BootPriest/BootModule structs |
| `shared/toml/parser.go` | Create | Shared TOML parser (kernel + fs module) |
| `kmazarin/ksyscall/loadmaz.go` | Modify | Add ET_EXEC path (loadOffset=0) |
| `cmd/maz-reloc/main.go` (or similar) | Modify | Accept ET_EXEC in addition to ET_DYN |
| `kmazarin/kmazarin/main.go` | Modify | Replace hardcoded launches with bootstrap loop |
| `kmazarin/ksyscall/launch_priest.go` | Create | SysLaunchPriest/SysStartPriest syscalls |
| `mazarin/sys/syscall.go` | Modify | Add SysLaunchPriest/SysStartPriest/SysLoadModuleIntoPriest |
| `Taskfile.yml` | Modify | Add fs-mzr-{arch} tasks, include in disk images |
| `flock/cmd/fat32/main.go` | Modify | SysLoadMaz delegation + application priest launch |
| `flock/cmd/disk/main.go` | Modify | Adapt to pre-loaded .mzr (no self-load) |

---

## Open questions

1. **How does the priest discover its pre-loaded modules?** Options:
   - Auxv entry with module base addresses (kernel writes before start)
   - Well-known symbol convention (priest links against `mzr_slot_0_base`)
   - The kernel calls the module's init function before starting the priest

2. **Should .mzr also go through maz-reloc for thin stubs?** Yes — the
   `.maz_imports` mechanism patches call sites regardless of PIE status.
   The `maz-reloc` tool needs to run on .mzr binaries too.

3. **Can a .mzr and a .maz coexist in the same priest?** Yes — they occupy
   different VA ranges. The .mzr is at its fixed slot address, the .maz is
   allocated above `priest.HighestVA`. No conflict.
