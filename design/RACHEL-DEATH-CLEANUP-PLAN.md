# Rachel — Shepherd Death Cleanup Plan

## Context

Rachel (window manager) needs to handle shepherd death properly. Currently `OnDeath`
only logs a message. When a shepherd dies, its windows remain on screen, backing store
pages leak, and the tracked state becomes stale.

Rachel should call `SysSubscribeDeaths` at startup to receive authoritative kernel-sourced
ProtoDeath notifications. She may also receive peer-level ProtoDeath from uring connections
— treat those as advisory (deduplicate, don't double-clean).

---

## Step 1: Register for Death Notifications

**File:** `flock/cmd/rachel/main.go` (around line 1584, before `disp.OnDeath`)

Call `sys.SubscribeDeaths()` during startup:

```go
if err := sys.SubscribeDeaths(); err != nil {
    fmt.Printf("[rachel] WARNING: SubscribeDeaths failed: %v\n", err)
}
```

This ensures the kernel sends ProtoDeath to rachel's uring ring for every shepherd death,
not just peers with direct uring connections.

---

## Step 2: Route Death Through wmCh

**File:** `flock/cmd/rachel/main.go`

The `OnDeath` callback runs in the uring reader goroutine. All window state is accessed
from the wmEventLoop goroutine (single-goroutine safety). Route through wmCh:

Define a sentinel type near the top of main.go:

```go
// shepherdDeath is routed through wmCh so death cleanup runs in the event loop goroutine.
type shepherdDeath struct {
    sid int16
}
```

Update `disp.OnDeath` (line 1585) to send through wmCh:

```go
disp.OnDeath(func(deadSID int16) {
    wmCh <- shepherdDeath{sid: deadSID}
})
```

Note: `wmCh` is `chan any` — the channel is already typed to accept arbitrary values.

---

## Step 3: Handle Death in wmEventLoop

**File:** `flock/cmd/rachel/main.go` (inside the `select` / `case raw := <-wmCh` block)

Before the `wmMsg, ok := raw.(wm.WMNotifyMsg)` check (line 1144), add a type switch
for `shepherdDeath`:

```go
case raw := <-wmCh:
    rachelNotifyCount++

    // Handle shepherd death (kernel-authoritative, routed from OnDeath).
    if death, ok := raw.(shepherdDeath); ok {
        handleShepherdDeath(int(death.sid))
        continue
    }

    wmMsg, ok := raw.(wm.WMNotifyMsg)
    ...
```

---

## Step 4: Implement handleShepherdDeath

**File:** `flock/cmd/rachel/main.go` (new function, near `removeFromZOrder`)

```go
func handleShepherdDeath(deadSID int) {
    ta := trackedApps[deadSID]
    if ta == nil {
        return // not a windowed shepherd, or already cleaned up
    }
    fmt.Printf("[rachel:wm] cleaning up dead shepherd SID %d\n", deadSID)

    // 1. Revoke focus if the dead shepherd has it.
    if keyboardFocusSID == deadSID {
        revokeFocus()
    }

    // 2. Tear down overlay if active.
    if ta.overlayActive {
        teardownOverlay(deadSID, ta)
    }

    // 3. Unregister all animations for this SID.
    cleanupAnimationsForShepherd(deadSID)

    // 4. Remove from z-order.
    removeFromZOrder(deadSID)

    // 5. Free backing store pages back to the kernel.
    if ta.backingStore != nil {
        mem.FreePages(unsafe.Pointer(&ta.backingStore[0]), len(ta.backingStore)/4096)
    }
    ta.backingStore = nil
    ta.decorFocused = nil
    ta.decorUnfocused = nil

    // 6. Remove from tracked apps.
    delete(trackedApps, deadSID)

    // 7. Repaint: clear to desktop BG, composite all living windows, flush.
    timedBlitAllWindows()
}
```

---

## Step 5: Add cleanupAnimationsForShepherd

**File:** `flock/cmd/rachel/animation.go` (new function)

Walk the animations slice and remove all entries belonging to the dead SID.
Also clean up any keyRepeatByCode entries that reference removed animations:

```go
// cleanupAnimationsForShepherd removes all animations belonging to the given SID.
func cleanupAnimationsForShepherd(sid int) {
    // Remove keyRepeatByCode entries for this SID's animations.
    for code, animID := range keyRepeatByCode {
        for _, a := range animations {
            if a.id == animID && a.targetSID == sid {
                delete(keyRepeatByCode, code)
                break
            }
        }
    }

    // Remove all animations for this SID.
    n := 0
    for _, a := range animations {
        if a.targetSID != sid {
            animations[n] = a
            n++
        }
    }
    animations = animations[:n]
}
```

---

## Step 6: Deduplicate Peer ProtoDeath

Rachel may receive two ProtoDeath messages for the same shepherd:
1. From `SysSubscribeDeaths` (kernel → all subscribers) — authoritative
2. From uring peer death (kernel → connected peers) — advisory

Both arrive as ProtoDeath on the same uring ring. The dispatcher's `OnDeath` handler
fires for each. Since they both route through wmCh → `handleShepherdDeath`, the
deduplication is natural: the second call finds `trackedApps[deadSID] == nil` and returns
immediately. No extra tracking needed.

---

## Step 7: SysFreePages Syscall

Backing store pages are allocated by rachel via `mem.AllocPagesSlice(PageShared)`.
These are kernel-tracked pages allocated through `SysAllocPages` — they are NOT Go heap
memory and the GC does not manage them. Without an explicit free, they leak in rachel's
address space on every shepherd death.

### Syscall Definition

**Slot:** 2 (freed, was `SysBootstrapRunElf`)

**File:** `shared/mazzy/mazzy.go`

```go
SysFreePages = MazzySyscallBase + 2  // 0x1002 - Free previously allocated pages
```

**Signature:** `SysFreePages(baseVA, numPages, 0, 0, 0, 0) → 0 or -errno`

- `arg0` = base VA (must be page-aligned, must be within caller's address space)
- `arg1` = number of pages to free

### Kernel Implementation

**File:** `kmazarin/ksyscall/free_pages.go` (new file)

The inverse of `SyscallAllocPages`. For each page in the range:
1. Walk the caller's page table to find the PA at this VA
2. Validate the PA is non-zero (page is actually mapped)
3. Unmap the page from the caller's address space (`kmem.UnmapUserPageWithL0`)
4. Release the physical frame (`kmem.ReleasePageByPA`)
5. After all pages: remove the span from the caller's `Spans`
6. TLB invalidate

The existing `unmapUserPages` in `runmaz.go` (line 259) does exactly this.
`SyscallFreePages` can delegate to it:

```go
func SyscallFreePages(arg0, arg1, _, _, _, _ uint64) int64 {
    baseVA := uintptr(arg0)
    numPages := int(arg1)

    if baseVA == 0 || baseVA&0xFFF != 0 {
        return -22 // EINVAL
    }
    if numPages < 1 || numPages > MaxAllocPages {
        return -22 // EINVAL
    }

    shepherd := proc.CurrentShepherd()
    if shepherd == nil {
        return -1 // EPERM
    }

    unmapUserPages(baseVA, numPages, shepherd.PageTableL0PA, int16(shepherd.PID))

    kmem.TlbiVMALLE1()
    kmem.DsbISH()
    kmem.IsbSY()

    return 0
}
```

**File:** `kmazarin/ksyscall/mazzy.go` — add dispatch entry:

```go
2: SyscallFreePages,  // FreePages = 0x1002
```

### Userspace Wrapper

**File:** `mazarin/mem/alloc.go` (extend existing file)

```go
// FreePages releases pages previously allocated by AllocPages/AllocPagesSlice.
// The VA must be the exact base returned by AllocPages. The count must match
// the original allocation (or be a subset starting from the base).
func FreePages(ptr unsafe.Pointer, count int) error {
    r1, _, errno := syscall.RawSyscall6(mazzy.SysFreePages,
        uintptr(ptr),
        uintptr(count),
        0, 0, 0, 0)
    if errno != 0 || int64(r1) < 0 {
        return errors.New("FreePages failed")
    }
    return nil
}
```

### Shared Page Consideration

When rachel allocates backing store pages and shares them via `SharePagesWithTarget`,
the pages are mapped into both rachel's and the client's address space. When the client
dies, the kernel unmaps the client's mapping during `CleanupShepherdPages`. Rachel's
mapping persists until `FreePages` is called. This is correct — rachel owns the pages.

When the client shepherd has already died, `FreePages` safely unmaps rachel's mapping
and releases the physical frames. The client's stale mapping (if any) was already cleaned
up by the kernel during `TerminateShepherd`.

---

## Files Changed

| File | Change |
|------|--------|
| `flock/cmd/rachel/main.go` | `shepherdDeath` type, `sys.SubscribeDeaths()` call, `OnDeath` routes through wmCh, death type-check in event loop, `handleShepherdDeath` function with `mem.FreePages` call |
| `flock/cmd/rachel/animation.go` | `cleanupAnimationsForShepherd` function |
| `shared/mazzy/mazzy.go` | `SysFreePages` constant (slot 2) |
| `kmazarin/ksyscall/free_pages.go` | New file: `SyscallFreePages` handler |
| `kmazarin/ksyscall/mazzy.go` | Dispatch table entry for slot 2 |
| `mazarin/mem/alloc.go` | `FreePages` userspace wrapper |

---

## Known Limitations

- **Constraint attribute cleanup**: `ta.bounds`, `ta.titleAttr`, `ta.bgColorAttr` are
  kernel-side attributes. They should be cleaned up with `SysAttrDelete` if the attributes
  are owned by rachel. Verify whether the kernel auto-cleans attributes owned by the dead
  shepherd.

---

## Verification

1. `$GO tool task` — build all architectures
2. `$GO tool task run-arm64-hvf TIMEOUT=45` — normal boot, verify all windows appear
3. Kill a shepherd (e.g. exit helloworld) — verify:
   - Window disappears immediately
   - Desktop background is repainted in the vacated area
   - Other windows remain and are fully redrawn
   - No crash, no stale pick rects, no orphaned focus
4. Kill the focused shepherd — verify focus revokes cleanly
5. Verify page accounting: after shepherd death + FreePages, the freed pages
   should be visible in `kmem` pool stats (check serial log for frame pool counts)
