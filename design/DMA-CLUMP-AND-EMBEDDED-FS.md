# DMA Clump Architecture + Embedded fs Shepherd

## Status: APPROVED (discussed 2026-03-26)

This plan has been extensively discussed and approved as an architectural change.
Implementers may proceed without further architectural discussion for the items
described here. Changes BEYOND this plan's scope still require discussion first.

---

## Overview

Replace the fixed 8-page kernel DMA pool with a flexible, userspace-driven
"clump" model. Physically contiguous pages are allocated by userspace via a new
`MAZARIN_CONTIGUOUS` mmap flag, automatically registered as DMA-capable, and
tracked per-shepherd in a kernel-side linked list. fs becomes a first-class
shepherd embedded in the kernel binary — no more disk.elf host process, no .maz
loading, no interface injection.

## Implementation Steps

### Step 1: `shared/dlist/` — DoublyLinkedList[T]

Generic doubly-linked list usable by both kernel and userspace. Go generics
(available since Go 1.18, project uses 1.25.5).

**Package:** `shared/dlist/`

**API:**
```go
type Node[T any] struct {
    Value T
    // unexported prev, next, list pointers
}
func (n *Node[T]) Next() *Node[T]
func (n *Node[T]) Prev() *Node[T]

type List[T any] struct { /* unexported head, tail, len */ }
func New[T any]() *List[T]
func (l *List[T]) PushBack(val T) *Node[T]
func (l *List[T]) PushFront(val T) *Node[T]
func (l *List[T]) Remove(n *Node[T]) T
func (l *List[T]) Front() *Node[T]
func (l *List[T]) Back() *Node[T]
func (l *List[T]) Len() int
func (l *List[T]) Range(fn func(*Node[T]) bool)  // safe to Remove current node
```

All heap-allocated. Standard Go GC manages lifetime. No OS dependencies.

---

### Step 2: DMA Clump Tracking

**New struct** (kernel-side, e.g. `kmazarin/proc/dma_clump.go`):
```go
type DMAClump struct {
    StartPA        uintptr
    StartVA        uintptr
    NumPages       int
    InFlight       int32    // atomic: incremented on BlockSubmit, decremented on completion
    ShepherdDead   bool     // set when owning shepherd exits
    PendingRelease bool     // set when munmap called with InFlight > 0
}
```

**Per-shepherd tracking** — add to `proc.ShepherdData`:
```go
DMAClumps dlist.List[DMAClump]
```

The kernel walks this list to find which clump a given VA belongs to (for
BlockSubmit PA resolution) and to manage lifecycle on munmap/death.

---

### Step 3: `MAZARIN_CONTIGUOUS` mmap Flag

**New constant** in `shared/constants/`:
```go
const MAZARIN_CONTIGUOUS = 0x200000  // bit 21, above all Linux MAP_* flags
```

**Kernel behavior** when this flag is set on mmap:
1. Calculate buddy allocator order = ceil(log2(numPages))
2. `AllocContiguousPages(order)` — returns base PA of 2^order contiguous pages
3. Pre-map ALL PTEs at mmap time (Normal-cacheable, Inner Shareable)
   - No demand paging — pages are backed and mapped before mmap returns
   - Userspace accesses will NOT fault; DMA can target these pages immediately
4. Create `DMAClump{StartPA, StartVA, NumPages}`, push to shepherd's list
5. Return VA to userspace

The buddy allocator inherently supports contiguous allocation — a block at
order N is 2^N contiguous pages. May need to add/expose `AllocContiguousPages`
and `FreeContiguousPages` if not already present.

---

### Step 4: munmap Integration

When `SyscallMunmap` is called, check if the VA range overlaps any DMA clump
in the calling shepherd's `DMAClumps` list:

- **Overlap, InFlight == 0:** Remove clump from list, unmap PTEs, free
  contiguous pages back to buddy allocator.
- **Overlap, InFlight > 0:** Set `PendingRelease = true`, unmap PTEs (so
  userspace can't access), but defer page release until the completion handler
  sees InFlight drop to 0.
- **No overlap:** Normal munmap path (unchanged).

---

### Step 5: BlockSubmit Update

Currently BlockSubmit only accepts VAs within the old fixed DMA pool. Change to:

1. Walk calling shepherd's `DMAClumps` list
2. Find clump where `clump.StartVA <= bufVA < clump.StartVA + clump.NumPages*4096`
3. Compute PA = `clump.StartPA + (bufVA - clump.StartVA)`
4. `atomic.AddInt32(&clump.InFlight, 1)`
5. Build descriptor chain with the computed PA and submit

Return error if bufVA is not within any registered clump.

---

### Step 6: Completion Handler Update

In the IRQ top-half (NonTimerIRQTopHalf), after draining the used ring:

1. Look up which clump the completed I/O belongs to (via stored metadata)
2. `atomic.AddInt32(&clump.InFlight, -1)`
3. If `InFlight == 0 && (ShepherdDead || PendingRelease)`:
   free the contiguous pages back to the buddy allocator and remove the clump

---

### Step 7: Remove Old Fixed 8-Page DMA Pool

Delete:
- `SysRegisterDMAPool` (syscall 0x1035) and `SysUnregisterDMAPool` (0x1036)
- `DMAPool` struct in `kmazarin/proc/proc.go`
- Fixed-pool PA lookup in BlockSubmit handler
- `mazarin/sys/` wrappers: `RegisterDMAPool()`, `UnregisterDMAPool()`
- The 8-page mmap + RegisterDMAPool code in `flock/cmd/fat32/main.go`

DMA buffer allocation is now entirely userspace's responsibility via
`MAZARIN_CONTIGUOUS`. The kernel just tracks clumps.

Free syscall slots 0x1035 and 0x1036 for future use.

---

### Step 8: Mazarin Library — ReadFilePages / LoadWholeFile

**New file:** `mazarin/sys/readfile.go`

```go
// ReadFilePages reads file data into caller-provided DMA pages.
// destVA must point to MAZARIN_CONTIGUOUS pages.
//
// Parameters:
//   destVA     — VA of destination buffer (physically contiguous, DMA-registered)
//   destSize   — buffer size in bytes
//   path       — file path on FAT32
//   fileOffset — byte offset into file to start reading
//   readLen    — bytes to read (-1 = fill buffer or until EOF)
//
// Returns (bytesRead, error).
func ReadFilePages(destVA uintptr, destSize int, path string,
    fileOffset int64, readLen int) (int, error)

// LoadWholeFile reads an entire file. Convenience: offset=0, readLen=-1.
func LoadWholeFile(path string, destVA uintptr, destSize int) (int, error)
```

**Implementation** (inside fs shepherd, serving requests via delegate):
- Open file in FAT32, walk FAT chain to map clusters to LBAs
- Issue up to **8 concurrent BlockSubmit** requests targeting sequential
  offsets within the destination buffer
- Track in-flight count and per-IOTag offset mapping
- As completions arrive via TrySoftIRQ, decrement in-flight, submit next batch
- Continue until readLen bytes transferred or EOF

**New error constants** in `mazarin/error/codes.go`:
- `DMAFailed` — BlockSubmit or completion returned an error
- `BufferTooSmall` — destination buffer smaller than requested read

---

### Step 9: fs Shepherd Rewrite

fs (currently `flock/cmd/fat32/`) becomes a full shepherd, not a .maz module.

**Startup sequence:**
```
1. Register for block device soft IRQ
2. Allocate scratch DMA buffer via mmap(MAZARIN_CONTIGUOUS) for FAT32 metadata
3. Mount FAT32 using async DMA reads into scratch buffer
4. sys.SetReady(true)
5. Launch client goroutine for TOML-driven shepherd loading
6. Enter delegate serve loop (LoadFile, ReadFilePages)
```

**Client goroutine** (step 5) acts as example userspace program:
- Read /kmazarin.toml via ReadFilePages into a small contiguous buffer
- Parse TOML, iterate [[shepherd]] entries
- For each shepherd: allocate contiguous pages via MAZARIN_CONTIGUOUS,
  call LoadWholeFile to read the ELF, pass loaded pages to RunShepherd
- munmap the pages after RunShepherd returns (kernel may also clean up)

This makes fs the first "real" userspace driver.

---

### Step 10: RunShepherd Optimization

Currently `DoRunShepherdWork` calls `copyPagesFromUser` to make a kernel heap
buffer, then `loadELF` copies byte-by-byte into the new shepherd's pages.

With MAZARIN_CONTIGUOUS pages, the kernel can skip the intermediate buffer:
- The caller's pages are physically contiguous with known PAs (from the clump)
- The kernel reads ELF data directly via the linear map:
  `kernelVA = clump.StartPA + offset + KernelLinearMapBase`
- `loadELF` reads from this kernel VA instead of from a `[]byte` buffer
- After loading, the kernel unmaps/frees the caller's pages as before

This eliminates one full copy of the ELF data (often 3-4MB per shepherd).

---

### Step 11: Shepherd Death Cleanup

When a shepherd exits or is killed:

1. Walk the shepherd's `DMAClumps` list
2. For each clump: set `ShepherdDead = true`
3. If `InFlight == 0`: free pages immediately, remove clump
4. If `InFlight > 0`: leave clump in list — the completion handler (Step 6)
   will free pages when the last in-flight I/O completes

Since block I/O completes in microseconds and cleanup runs on a separate
kernel goroutine, the deferred-release window is tiny.

---

### Step 12: Embed fs.elf in kmazarin.elf

**Separate but necessary.** Eliminates the circular bootstrap dependency where
the kernel reads fs from the same filesystem that fs implements.

**Mechanism:** `go:embed`

```go
// kmazarin/kmazarin/embedded_fs.go
package main

import _ "embed"

//go:embed fs.elf
var EmbeddedFSElf []byte
```

**Build order** (Taskfile):
```
fs-elf → stage-embedded-fs → kmazarin-arm64
```
`stage-embedded-fs` copies the built fs.elf into `kmazarin/kmazarin/` where
`go:embed` can find it. `kmazarin/kmazarin/fs.elf` is gitignored.

**Kernel boot sequence** changes to:
```
1. Hardware init
2. loadELF(EmbeddedFSElf) — launch fs from memory, zero disk I/O
3. WaitForShepherdReady("fs")
4. Read /kmazarin.toml via fs (now using async DMA)
5. Apply config, launch [[shepherd]] entries via fs
```

**What gets deleted:**
- `flock/cmd/disk/` — disk.elf shepherd (entire directory)
- `SyscallBootstrapRunElf` — kernel no longer reads from disk at boot
- `[[bootstrap_shepherd]]` TOML section and parser support
- `LoadMazBootstrap`, `MazarinShepherd` injection, `forceBlockDevItab`
- `preGrowStack` workaround
- Kernel-side FAT32 mount code in boot path
- fs.maz / fs.mzr build tasks (fs is now a shepherd ELF)
- `injectedBlockDev` global in fat32/main.go

**TOML simplifies to:**
```toml
[[shepherd]]
name = "rachel"
path = "/rachel.elf"

[[shepherd]]
name = "clocks"
path = "/clocks.elf"
```

---

## Key Design Decisions (Already Discussed)

- **Userspace owns DMA pages.** The kernel tracks them but does not allocate
  them on behalf of userspace (except via the mmap mechanism).
- **MAZARIN_CONTIGUOUS pre-maps all PTEs.** No demand paging for DMA pages.
  Pages are backed at mmap time so DMA can target them immediately.
- **InFlight counter per clump** is the mechanism for knowing when deferred
  release is safe.
- **8 concurrent BlockSubmit requests** for multi-flight I/O.
- **DoublyLinkedList[T] is shared** (`shared/dlist/`), not kernel-only.
- **fs is the first real userspace driver** — it allocates its own DMA buffers
  and drives the block device directly.

## Dependencies

```
Step 1 (DoublyLinkedList) ─────────────────────┐
Step 2 (DMA clump struct) ← Step 1             │
Step 3 (MAZARIN_CONTIGUOUS) ← Step 2           │
Step 4 (munmap integration) ← Step 3           │
Step 5 (BlockSubmit update) ← Step 3           │
Step 6 (completion handler) ← Step 5           │
Step 7 (remove old DMA pool) ← Steps 3,5,6    │
Step 8 (ReadFilePages library) ← Steps 5,6    │
Step 9 (fs rewrite) ← Steps 7,8               │
Step 10 (RunShepherd optimization) ← Step 3    │
Step 11 (shepherd death cleanup) ← Steps 2,6   │
Step 12 (embed fs.elf) ← Step 9               │
```

Steps 1-6 are foundational. Step 7 is cleanup. Steps 8-9 are the user-visible
feature. Steps 10-11 are optimizations. Step 12 is the final boot simplification.
