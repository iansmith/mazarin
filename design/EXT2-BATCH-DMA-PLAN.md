# Plan: Async DMA for ext2 — A then C

## Problem

Every ext2 metadata operation — reading an inode, listing a directory, resolving
indirect blocks — goes through `readBytes` → `ReadBlock` one block at a time.
Each `ReadBlock` is a full VirtIO DMA round-trip: submit descriptor → WFI →
interrupt → copy. The async batching infrastructure is only used for
`readFileIntoPages` in the fs shepherd.

## Stage A: Add `ReadBlocks` to BlockDevice Interface

### A1: Interface + default implementation

Add to `shared/blockdev/blockdev.go`:

```go
type BatchBlockDevice interface {
    BlockDevice
    ReadBlocks(lbas []uint64, dst []byte) error
}
```

Separate interface so existing `BlockDevice` consumers don't break.
`MemBlockDevice` gets a `ReadBlocks` that loops over `ReadBlock`. The fs
shepherd's `asyncBlockDev` already has `readBatch` — adapt it to satisfy
`BatchBlockDevice`.

**A1 test**: New test in `shared/blockdev/blockdev_test.go` — write known
patterns to a `MemBlockDevice`, call `ReadBlocks` with scattered LBAs, verify
each block in the output matches the corresponding `ReadBlock` result. Also test
edge cases: zero-length LBA list, single LBA.

### A2: ext2 detects and uses BatchBlockDevice

Add `readBlocks(blockNums []uint32, dst []byte) error` to `FileSystem`. It
converts fs block numbers to device LBAs. If `device` satisfies
`BatchBlockDevice`, calls `ReadBlocks`. Otherwise falls back to N × `readBlock`.

**A2 test**: New test in `shared/fs/ext2/reader_test.go` — create a
`countingBlockDev` wrapper that counts calls to `ReadBlock` vs `ReadBlocks`.
Mount ext2 on it. Read a multi-block file. Verify `ReadBlocks` was called (not N
individual `ReadBlock` calls). This proves the plumbing works without needing any
ext2 call sites converted yet.

## Stage C: Convert ext2 Read Paths

Each substage converts one call site. After each, the existing test suite must
pass (`go test ./shared/fs/ext2/ -v`). The counting wrapper verifies the
optimization actually kicked in.

### C1: `ReadDir` → batch

Current code loops: `inodeBlockNum` → `readBlock` per directory block.
New code: compute `blocksUsed` from inode, call `ResolveBlockList(inode, 0,
blocksUsed)` to get all block numbers, call `readBlocks(blockNums, bigBuf)`,
then parse entries from the contiguous buffer.

**C1 test**: Existing `TestReadRootDir`, `TestReadSubdir`, `TestNestedDirRead`
pass unchanged. Add `TestReadDirBatchCount` with counting wrapper: directory
spanning 2+ blocks, verify `ReadBlocks` called once.

### C2: `File.ReadAll` → batch

Current code: `Read(result)` which loops one block at a time.
New code: `ResolveBlockList(inode, 0, totalBlocks)` → `readBlocks(blocks,
result)`. Handle sparse blocks (block number 0 → zero-fill that region).

**C2 test**: Existing `TestReadFileContents`, `TestReadLargeFile` pass
unchanged. Add `TestReadAllBatchCount`: 200KB file, verify `ReadBlocks` called
once with ~50 block numbers.

### C3: `File.Read` (streaming) — skip

Leave `Read` as-is. The block cache already avoids re-reading the same block.
Sequential `Read` with a large buffer already hits each block once. Callers that
read entire files use `ReadAll`. The complexity of a windowed prefetch isn't
justified for small sequential reads (config files, etc.).

### C4: `loadBitmaps` → batch

Current code reads 2 blocks per group (block bitmap + inode bitmap) in a loop.
New code: collect all bitmap block numbers across all groups, single
`readBlocks` call, then slice the result into per-group bitmaps.

**C4 test**: Existing `TestWriteAndRead` (writer_test.go) exercises `MountRW` →
`loadBitmaps`. Passes unchanged. Count check: for N groups, 1 `ReadBlocks` call
not 2N `ReadBlock` calls.

### C5: Collapse `readFileIntoPages` into ext2

Currently `readFileIntoPages` in `flock/cmd/fs/main.go` bypasses ext2 — calls
`ResolveBlockList` then `blkDev.readBatch` directly. With `ReadAll` now batched
(C2), both ramdisk and DMA paths flow through ext2:

Add `File.ReadInto(dst []byte) (int, error)` — does the `ReadAll` logic into a
caller-provided buffer (avoids allocation). `readFileIntoPages` calls
`file.ReadInto(dst[:fileSize])`.

**C5 test**: Full boot test — `$GO tool task run-arm64-hvf TIMEOUT=60`.

## Summary Table

| Stage | Files Changed | Test |
|-------|--------------|------|
| A1 | `blockdev.go`, `memblockdev.go` | New `blockdev_test.go`: ReadBlocks == N×ReadBlock |
| A2 | `reader.go` | New counting wrapper test: readBlocks plumbing works |
| C1 | `reader.go` (ReadDir) | Existing dir tests pass + batch count test |
| C2 | `file.go` (ReadAll) | Existing file tests pass + batch count test |
| C3 | skip | — |
| C4 | `reader.go` (loadBitmaps) | Existing write tests pass + batch count test |
| C5 | `fs/main.go`, `file.go` | Boot test: `run-arm64-hvf TIMEOUT=60` |
