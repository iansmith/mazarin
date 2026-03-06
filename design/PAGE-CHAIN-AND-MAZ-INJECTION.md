# Page Chain IPC and .maz Service Injection

## Overview

This document describes the design for multi-priest page-passing chains
(used for filesystem I/O) and the mechanism for injecting host services
into dynamically loaded .maz modules.

**Decision: Option A (nested delegation) for the initial implementation.**
Option B (direct L4-style IPC between priests) is the long-term target.
The client-side library abstraction (`mazarin/mazhost`, `mazarin/mazguest`)
hides the mechanism so we can switch without changing priest code.

---

## Read Chain: caller → stdio → fs.maz → disk → kernel DMA

```
caller calls read(fd, buf, 4096)
    │
    ▼  delegation (existing infrastructure)
┌─────────────────────────────────────────────┐
│  KERNEL                                     │
│  Allocates page, maps into stdio            │
│  Blocks caller in ThreadBlockedDelegate     │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│  STDIO PRIEST                               │
│  Receives SyscallRequest{SysID=Read}        │
│  Special-cases /dev/random (handle locally) │
│  For files: makes SysFilesystemRead()       │
│  which is itself delegated → disk priest    │
│  stdio blocks in nested delegation          │
└──────────────────┬──────────────────────────┘
                   ▼  nested delegation (Option A)
┌─────────────────────────────────────────────┐
│  KERNEL                                     │
│  SysPageGrant: maps page into disk priest   │
│  Delegates SysFilesystemRead to disk priest │
│  Blocks stdio in ThreadBlockedDelegate      │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│  DISK PRIEST                                │
│  Receives SyscallRequest{SysFilesystemRead} │
│  Calls into fs.maz via Go interface:        │
│    fs.ReadFile(path, page)                  │
│  fs.maz calls blockDev.ReadBlocks() which   │
│  is a direct Go call to the disk priest's   │
│  VirtIO block driver                        │
│  Disk priest issues DMA, waits for IRQ      │
│  Data lands in the page                     │
│  Replies to kernel                          │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│  KERNEL unwinds:                            │
│  1. Revokes page from disk priest           │
│  2. Wakes stdio with reply                  │
│  3. stdio replies to original delegation    │
│  4. Kernel copies page to caller's buffer   │
│  5. Reclaims page, wakes caller             │
└─────────────────────────────────────────────┘
```

---

## .maz Service Injection

### Problem

fs.maz runs inside the disk priest's address space (same goroutines,
same page table). It needs to call disk functions directly — no syscalls,
just Go function calls via an interface. But .maz has its own data segment,
so the disk priest can't set globals in fs.maz's copy of a shared package.

### Solution: pclntab symbol resolution + init function convention

**Shared interface** (`shared/blockdev/blockdev.go`):
```go
package blockdev

type Device interface {
    ReadBlocks(startBlock uint64, count int, buf []byte) (int, error)
    WriteBlocks(startBlock uint64, count int, buf []byte) (int, error)
    BlockSize() uint64
    BlockCount() uint64
}
```

**fs.maz exports an Init function:**
```go
package main

import "mazzy/shared/blockdev"

var dev blockdev.Device

func Init(d blockdev.Device) { dev = d }

func main() {
    // dev is already set by Init
    // start serving filesystem requests
}
```

**Disk priest loads and injects:**
```go
result, _ := sys.LoadMaz("/fs.maz")
sys.RegisterMazModule(result)

// Resolve fs.maz's Init function from pclntab
initPC := sys.MazLookupFunc(result, "main.Init")
type funcval struct{ fn uintptr }
fv := &funcval{fn: initPC}
init := *(*func(blockdev.Device))(unsafe.Pointer(&fv))

// Inject our block device implementation
init(&diskDevice{})

// Launch fs.maz's main
fv2 := &funcval{fn: uintptr(result.EntryPoint)}
go (*(*func())(unsafe.Pointer(&fv2)))()
```

### Client-side library abstraction

Hide the injection mechanism behind `mazarin/mazhost` and `mazarin/mazguest`
so we can later move to kernel-mediated injection without changing priest code:

```go
// mazarin/mazhost/mazhost.go — host side (disk priest)
func LoadAndInit(path string, services ...any) (*MazInstance, error)

// mazarin/mazguest/mazguest.go — guest side (fs.maz)
func GetService[T any]() T
```

### New kernel API needed

**`SysMazLookupFunc`** (Mazzy syscall):
- Input: MazResult (or load base), function name string
- Output: PC address of the named function
- Implementation: walk the .maz's moduledata ftab entries, match name
  from pclntab funcname data

---

## Page Ownership Ledger (Crash Safety)

When a page passes through multiple priests, each holder is tracked:

```go
type PageChainEntry struct {
    PA         uintptr
    OriginTID  int32       // original caller blocked on this page
    OriginPID  int16
    Holders    [4]struct { // PIDs with this page mapped
        PID int16
        VA  uint64
    }
    NumHolders int
}
```

**TerminatePriest crash cleanup:**
1. Scan ledger for entries where dead priest is a holder
2. Unmap page from all remaining holders
3. Release PA to page pool
4. Wake origin caller with -EIO

---

## New Kernel APIs Summary

| API | Type | Purpose |
|-----|------|---------|
| `SysMazLookupFunc` | Mazzy syscall | Resolve function name → PC from .maz pclntab |
| `SysPageGrant` | Mazzy syscall | Map caller's page into another priest's VA space |
| `SysPageRevoke` | Mazzy syscall | Unmap a previously granted page |
| `SysFilesystemRead` | Mazzy syscall | Read from filesystem (delegated to disk priest) |
| `SysFilesystemOpen` | Mazzy syscall | Open file (delegated to disk priest) |
| Page ownership ledger | Kernel internal | Track PA → holders for crash cleanup |

---

## TODOs

- [ ] Special-case `/dev/random` in stdio priest (handle locally, no fs delegation)
- [ ] Implement `SysMazLookupFunc` syscall
- [ ] Create `shared/blockdev/` interface package
- [ ] Create `mazarin/mazhost/` and `mazarin/mazguest/` abstraction libraries
- [ ] Implement `SysPageGrant` / `SysPageRevoke` syscalls
- [ ] Implement page ownership ledger in kernel
- [ ] Add crash cleanup to `TerminatePriest`
- [ ] Register `SysFilesystemRead` / `SysFilesystemOpen` as delegated syscalls
- [ ] Build fs.maz with FAT32 implementation
- [ ] Wire disk priest to load fs.maz and inject block device interface
