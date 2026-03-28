# Linux Shepherd — Phase 5 & 6 Continuation Prompt

Use this as a continuation prompt for Claude Code. Paste everything below the line.

---

## Context: Linux Shepherd Implementation

We are building a userspace "linux" shepherd (renamed from "stdio") that handles Linux file syscalls via the existing delegate-for-syscalls infrastructure. The shepherd manages an FD table in userspace, maps ext2 errors to Linux errno values, and converts C-style null-terminated strings.

### What is complete (Phases 1–4):

1. **Phase 1 — Rename**: `stdio` → `linux` across entire codebase (Taskfile, TOML configs, .gitignore, all Go source comments/variable names). Directory is `flock/cmd/linux/`.

2. **Phase 2 — Sysid entries**: 22 new syscall IDs added to `shared/sysid/sysid.go` (Lseek, Fstat, Fstatat, Mkdirat, Unlinkat, Renameat, Ftruncate, Getdents64, Readlinkat, Faccessat, Fchmodat, Utimensat, Getcwd, Chdir, Fchdir, Ioctl, Writev, Readv, Statfs, Fstatfs, Fsync, Fdatasync) with correct per-architecture Linux syscall number mappings in `kmazarin/ksyscall/translate_{arm64,riscv64,amd64}.go`.

3. **Phase 3 — Supporting files**:
   - `flock/cmd/linux/fdtable.go` — FD table with fdKind (None/Stdin/Stdout/Stderr/File/Dir), fdEntry (kind + ext2.FD + flags), alloc/get/put/free/resolvePath
   - `flock/cmd/linux/errno.go` — Linux errno constants + `ext2Errno(err) int64` mapping
   - `mazarin/cstr/cstr.go` — C string ↔ Go string conversion (ToGo, FromGo)
   - `kmazarin/ksyscall/delegate.go` — Updated kernel delegation with per-syscall CallerBufVA/CallerBufLen for copy-back syscalls (Fstat→128 bytes, Fstatfs→120, Fstatat→arg2/128, Getcwd→arg0/arg1, Readlinkat→arg2/arg3)

4. **Phase 4 — Syscall handler + wiring**:
   - `flock/cmd/linux/syscalls.go` — Full `syscallHandler` struct with `handle(req)` dispatch to 26 handler methods:
     - Local-only: sysClose, sysLseek, sysGetcwd, sysChdir, sysFchdir, sysIoctl, sysFsync
     - Metadata: sysOpenat (with O_CREAT), sysFstat, sysFstatat, sysMkdirat, sysUnlinkat, sysRenameat (ENOSYS stub—needs two-string support), sysFtruncate (ENOSYS stub), sysGetdents64, sysFaccessat, sysFchmodat, sysUtimensat, sysReadlinkat (EINVAL—no symlinks), sysStatfs/sysFstatfs (ENOSYS stubs)
     - Data: sysRead, sysWrite, sysWritev/sysReadv (ENOSYS stubs)
     - Helper: `fillStatBuf(buf, inode, inum)` — 128-byte Linux struct stat
   - `flock/cmd/linux/main.go` — `newSyscallHandler()` created, all 26 sysids registered via `HandleSyscalls(...)`, `startDelegateHandler` routes Write fd 0/1/2 to console display, everything else to `handler.handle(req)`
   - `shared/fs/ext2/writer.go` — Exported `resolveInode` → `ResolveInode`
   - `shared/fs/ext2/reader.go` — Added `OpenInum(inum uint32) (*File, error)`
   - `shared/fs/ext2/file.go` — `Seek` now allows past-EOF (matches Linux lseek)

**Current state**: Everything compiles on arm64, amd64, riscv64. ext2 tests pass. But `handler.fs` is nil — no filesystem is mounted, so file syscalls return ENOSYS/EINVAL.

### Key architecture decisions (user-directed):

- **FD table is userspace-side**, not in kernel
- **Ramdisk at `/tmp`**, 128MB, real ext2 format, backed by a `MemBlockDevice`
- **Path resolution belongs in linux shepherd** (only place with CWD concept)
- **getdents64 is metadata path** (no DMA needed — directory data is already loaded)
- **stdout/stderr both go through ring buffer** (not blocking serial), displayed on screen, stderr in red
- **Ramdisk copy paths should use fast assembly memcpy** (ldp/stp on ARM64, vector on RISC-V) — this is a future optimization, not blocking
- **No references to "disk" shepherd or "fs.maz"** — `fs.elf` replaced both
- **readv/writev** primarily for future userspace net driver
- **No architecture changes without discussion first**
- **Polling or timeouts = architectural change** requiring discussion

### Phase 5: MemBlockDevice + ext2 ramdisk at /tmp

Create a memory-backed block device and mount a real ext2 filesystem on it:

1. **Create `shared/blockdev/memblockdev.go`** — `MemBlockDevice` implementing `blockdev.BlockDevice`:
   - Constructor: `NewMemBlockDevice(name string, blockSize, numBlocks uint64) *MemBlockDevice`
   - Backed by a `[]byte` slice (blockSize * numBlocks bytes)
   - ReadBlock/WriteBlock just copy from/to the slice at `lba * blockSize`
   - 128MB total = 128*1024*1024 bytes. With 512-byte blocks: 262144 blocks. Or with 4096-byte blocks: 32768 blocks.

2. **Create an in-memory ext2 image**: Use the existing `cmd/mkext2` tool (if it exists) or create a minimal ext2 superblock+group descriptors+bitmaps+root dir programmatically. The ext2 package already has `MountRW` which loads bitmaps. The simplest approach:
   - Check if `cmd/mkext2` exists and can format a byte slice
   - If not, create a `FormatMemDevice(dev blockdev.BlockDevice) error` function that writes a minimal ext2 filesystem (superblock, group descriptors, bitmaps, root inode with empty directory)

3. **Wire into `syscallHandler`**: In `main.go`, after creating the handler:
   - Create MemBlockDevice (128MB, 4096-byte blocks = 32768 blocks)
   - Format it as ext2
   - `ext2.MountRW(memDev)` → set `handler.fs`
   - Create `/tmp` directory on the mounted filesystem (or mount the ramdisk as the root)
   - Log: `[linux] ramdisk mounted at /tmp (128MB ext2)`

4. **Path routing in syscallHandler**: The ramdisk IS the filesystem for now. All file paths go to `handler.fs`. Later when fs shepherd IPC is added, paths under `/tmp` go to the ramdisk and other paths go through IPC to the fs shepherd.

### Phase 6: fs shepherd IPC (metadata ring + data channel)

This connects the linux shepherd to the `fs` shepherd for real disk access:

1. **Metadata ring**: 128-byte fixed-size slots with 64-bit request IDs. The linux shepherd sends file operation requests (open, stat, readdir, etc.) and the fs shepherd replies.

2. **Data channel**: For bulk read/write — DMA pages transferred between shepherds.

3. **Two IPC channels**: metadata ring for control plane, data channel for data plane.

4. **Path routing**: `/tmp/*` → ramdisk (handler.fs), everything else → fs shepherd IPC.

**Important**: Phase 6 requires understanding how the fs shepherd (`flock/cmd/fs/`) currently works. Read `flock/cmd/fs/main.go` to understand its IPC interface before designing the linux↔fs protocol.

### Key files to read:

- `flock/cmd/linux/main.go` — main shepherd, UI, delegate handler
- `flock/cmd/linux/syscalls.go` — all syscall handler methods
- `flock/cmd/linux/fdtable.go` — FD table
- `flock/cmd/linux/errno.go` — errno mapping
- `shared/fs/ext2/reader.go` — ext2 filesystem (Mount, ReadInode, Open, OpenInum, ResolveInode)
- `shared/fs/ext2/writer.go` — ext2 write ops (Create, Mkdir, Remove, Rename, Sync, MountRW)
- `shared/fs/ext2/file.go` — File Read/Write/Seek
- `shared/blockdev/blockdev.go` — BlockDevice interface
- `mazarin/cstr/cstr.go` — C string helpers
- `shared/sysid/sysid.go` — syscall ID definitions
- `kmazarin/ksyscall/delegate.go` — kernel-side delegation infrastructure

### Branch: `feature/disk-io-basic`

Start with Phase 5. Build `MemBlockDevice`, format ext2, mount it, and wire into `handler.fs` so file syscalls actually work against the ramdisk. Then move to Phase 6 if time permits.
