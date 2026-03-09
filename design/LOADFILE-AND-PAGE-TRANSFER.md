# LoadFile, Page Transfer, and Boot Restructuring Plan

## Overview

Restructure the boot sequence so that fs.maz (the FAT32 filesystem module loaded
into the disk priest) becomes the filesystem authority. File loading uses zero-copy
page transfer instead of copying bytes through syscalls. The kernel only reads from
disk once (to bootstrap fs.maz itself); after that, all file I/O goes through fs.maz.

## New Syscalls

### 1. TransferAndUnmap(targetPID, startVA, numPages, bytesRead)

General-purpose page ownership transfer between priests.

- Unmaps the specified pages from the caller's page table
- Maps the same physical pages into the target priest's address space (kernel picks the VA)
- Returns the new VA in the target priest's address space
- No data copying — same physical pages, just remapped
- Foundation for future IPC

**Kernel implementation:**
- Walk caller's page table to find physical addresses for the page range
- Unmap pages from caller's L0 page table
- Find a free VA range in target priest's address space
- Map the same physical pages into target's L0 page table
- Update target priest's HighestVA if needed
- Return the new VA

**Error conditions:**
- Target PID invalid
- startVA not page-aligned
- Pages not mapped in caller's address space
- No free VA range in target's address space

### 2. LoadFile(path) -> (startVA, numPages, bytesRead, err)

Syscall that any priest can call to read a file. Delegated to fs.maz via the
existing delegate mechanism.

**Caller's perspective:**
- Calls LoadFile with a path string
- Blocks until fs.maz delivers the pages
- Returns with file contents mapped as pages in the caller's own address space

**fs.maz's perspective (delegate handler):**
1. Receives delegated LoadFile request (includes caller PID, path)
2. Opens file on FAT32, gets file size
3. Calculates numPages = (fileSize + 4095) / 4096
4. Calls mmap(numPages * 4096) to get pages in disk priest's address space
5. Reads file data into those pages via BlockRead (synchronous, interrupt-driven)
6. Calls TransferAndUnmap(callerPID, startVA, numPages, bytesRead)
7. Replies via delegate mechanism with (newVA, numPages, bytesRead)

**Error conditions:**
- No delegate registered for LoadFile -> ErrNoDelegate
- Delegate priest not ready -> ErrNotReady
- File not found (fs.maz returns error)
- I/O error during read

### 3. RunMaz(startVA, numPages, totalBytes) -> (entryPoint, moduledataAddr, priestInitAddr, err)

Process pages in the caller's own address space as a .maz ELF. This is a
refactoring of the existing DoLoadMazWork — same ELF parsing, relocation,
and import resolution logic, but reading from the caller's own pages instead
of from disk.

**What the kernel does:**
- Reads ELF data from the caller's pages at startVA
- Parses ELF header (validates magic, class, machine, type)
- For ET_DYN (PIE): calculates load base above priest's current HighestVA
- For ET_EXEC: uses linked addresses
- Loads PT_LOAD segments into new pages in the caller's address space
- Applies PIE relocations (.rela.dyn)
- Resolves .maz_imports against caller's symbol table
- Finds entry point (main.MazarinMain or main), moduledata, MazarinPriest symbols
- **Implicitly unmaps the raw ELF pages** from the caller (they're temporary)
- Cache maintenance (invalidate icache)
- Returns entry point + metadata

**Caller usage:**
```go
va, pages, n, err := LoadFile("/helloworld.maz")
if err == nil {
    RunMaz(va, pages, n)
}
```

### 4. RunPriest(name, startVA, numPages, totalBytes) -> err

Create a new priest from an ELF in the caller's pages. Similar to RunMaz but
creates a new priest with its own address space.

**What the kernel does:**
- Creates a new Priest struct (allocates PID)
- Creates a new L0 page table for the priest
- Reads ELF data from the caller's pages at startVA
- Loads ELF segments into the new priest's page table
- Sets up priest's stack, heap, environment (GODEBUG=gctrace=1, GOGC=5)
- Registers the priest by name
- **Implicitly unmaps the raw ELF pages** from the caller
- Starts the priest's main thread

**Caller usage (fs.maz launching stdio):**
```go
va, pages, n, err := LoadFile("/stdio.elf")
if err == nil {
    RunPriest("stdio", va, pages, n)
}
```

### 5. SetReady(bool) -> err

Priest signals that it is ready to accept delegated work.

- Sets/clears an atomic int32 `Ready` field on the Priest struct
- Checked by the kernel before delegating LoadFile requests
- Visible in PriestInfo syscall response

## Kernel Boot Change

Modify `launchPriestsFromConfig` in `kmazarin/kmazarin/main.go`:
- Only launch `[[bootstrap_priest]]` entries (e.g., disk priest)
- Do NOT launch `[[priest]]` entries (dapope, stdio)
- fs.maz is responsible for reading TOML and launching non-bootstrap priests

## fs.maz Rewrite

Replace the current IPC-based serve loop in `flock/cmd/fat32/main.go` with:

### MazarinMain flow:
1. Mount FAT32 using injected BlockDevice (unchanged)
2. Register as delegate handler for LoadFile syscall
3. Call SetReady(true)
4. Spawn TOML walker goroutine (see below)
5. Enter delegate receive loop (main goroutine):
   - Receive delegated LoadFile requests
   - Post to buffered channel (cap 64)
   - Worker goroutine processes one at a time

### TOML walker goroutine:
1. Read `/kmazarin.toml` from FAT32
2. Parse with a real TOML library (pelletier/go-toml/v2 or BurntSushi/toml)
3. For each `[[priest]]` entry:
   - Read ELF from FAT32 into mmap'd pages
   - Call RunPriest(name, va, pages, n)
4. For each priest with `maz = [...]` entries:
   - Read .maz from FAT32 into mmap'd pages
   - Transfer pages to the target priest
   - (Target priest calls RunMaz when ready)

### Worker goroutine (channel consumer):
- Reads from buffered channel (cap 64), processes one LoadFile at a time
- Each request: open file on FAT32, get size, mmap pages, BlockRead, TransferAndUnmap, delegate reply
- Serialized — only one disk operation at a time (matches single blockIOBlockedTID slot)

## Implementation Order

1. **TransferAndUnmap** — kernel syscall, page transfer primitive
   - New syscall number in mazarin/sys/syscall.go
   - Kernel implementation in kmazarin/ksyscall/
   - Userspace wrapper in mazarin/sys/

2. **LoadFile** — delegated syscall
   - New syscall number + sysid entry
   - Kernel dispatch: check delegate registered + ready, forward to fs.maz
   - Userspace wrapper in mazarin/sys/

3. **RunMaz** — refactor existing DoLoadMazWork
   - Change to read from caller's pages instead of from disk
   - Add implicit unmap of raw ELF pages
   - New syscall number (or reuse existing SysLoadMaz number)
   - Userspace wrapper in mazarin/sys/

4. **RunPriest** — new syscall
   - Similar to RunMaz but creates new priest
   - Reuses existing priest creation logic from launchPriestELF
   - Implicit unmap of raw ELF pages
   - New syscall number
   - Userspace wrapper in mazarin/sys/

5. **SetReady** — simple atomic flag syscall
   - Add Ready int32 to proc.Priest
   - New syscall number
   - Userspace wrapper in mazarin/sys/
   - Expose in PriestInfo

6. **fs.maz rewrite** — delegate-based, channel queue, TOML walker
   - Rewrite flock/cmd/fat32/main.go
   - Remove old IPC-based serveLoop
   - Add delegate receive loop + channel + worker goroutine
   - Add TOML walker goroutine
   - Use real TOML library

7. **Kernel boot change** — only bootstrap priests
   - Modify launchPriestsFromConfig to skip [[priest]] entries
   - fs.maz handles launching non-bootstrap priests

## Error Codes

| Name | Value | Meaning |
|------|-------|---------|
| ErrBadName | 0x100D | Empty or invalid name/path |
| ErrBadELF | 0x100E | Not a valid ELF, wrong arch |
| ErrMazLinkFailed | 0x100F | .maz dynamic linking failed |
| ErrNoDelegate | 0x1010 | No delegate registered for this syscall |
| ErrNotReady | 0x1011 | Delegate priest not ready |
| ErrTransferFailed | 0x1012 | Page transfer failed (bad VA, no space) |

## Existing Infrastructure That Gets Reused

- **Delegate mechanism** (kmazarin/ksyscall/delegate.go) — routes LoadFile to fs.maz
- **BlockForBlockIO / WakeBlockIOThread** (io_bridge.go) — interrupt-driven disk I/O
- **DoLoadMazWork ELF logic** (loadmaz.go lines 144+) — parsing, relocation, import resolution
- **Priest creation** (existing launchPriestELF path) — for RunPriest
- **mmap syscall** — fs.maz uses it to allocate pages
- **sys.BlockRead** — fs.maz reads disk sectors through the kernel's block driver

## Bootstrap Exception

The kernel retains the ability to load fs.maz from disk directly during early boot.
This is the ONE case where the kernel reads from disk. The disk priest uses the
existing SysLoadMaz (bootstrap variant) to load fs.maz before fs.maz exists.
After fs.maz is running, all file I/O goes through it.

## Key Design Properties

- **Zero-copy file loading**: Pages allocated once, transferred between priests, never copied
- **TransferAndUnmap is generic**: Same primitive works for LoadFile, RunMaz, RunPriest, and future IPC
- **Serialized disk I/O**: Channel (cap 64) in fs.maz ensures one disk op at a time
- **Fail-fast on not-ready**: LoadFile returns ErrNotReady if fs.maz hasn't called SetReady yet; caller retries
- **Implicit unmap**: RunMaz and RunPriest free the raw ELF pages automatically
- **fs.maz is the filesystem authority**: Only it knows how to map paths to disk blocks
