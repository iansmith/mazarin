package main

import (
	"fmt"
	"unsafe"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/sys"
	"mazzy/shared/dlist"
	"mazzy/shared/sysid"
)

// syscallHandler processes delegated file syscalls for the linux shepherd.
// Per-shepherd filesystem state (FD tables, flocks, CWD) is tracked in
// ShepherdFilesystemData, keyed by caller SID.
type syscallHandler struct {
	shepherds map[int16]*ShepherdFilesystemData
	flocks    *flockTable
	fs        *fsclient.Client
	cache     *pageCache
}

func newSyscallHandler(fs *fsclient.Client) *syscallHandler {
	return &syscallHandler{
		shepherds: make(map[int16]*ShepherdFilesystemData),
		flocks:    newFlockTable(),
		fs:        fs,
		cache:     newPageCache(),
	}
}

// getShepherd returns the per-shepherd filesystem state for the given SID,
// creating it lazily on first contact.
func (h *syscallHandler) getShepherd(sid int16) *ShepherdFilesystemData {
	s := h.shepherds[sid]
	if s == nil {
		s = &ShepherdFilesystemData{
			SID:   sid,
			FDT:   newFDTable(),
			Locks: dlist.New[*flockEntry](),
		}
		h.shepherds[sid] = s
	}
	return s
}

// cleanupShepherd closes all open FDs, releases all flocks, and removes
// the per-shepherd state for the given SID. Called on shepherd death.
func (h *syscallHandler) cleanupShepherd(sid int16) {
	s := h.shepherds[sid]
	if s == nil {
		return
	}
	// Release all advisory locks.
	h.flocks.releaseAll(s.Locks)
	// Close all open file handles (skip stdio fds 0-2).
	for fd := 3; fd < MaxFDs; fd++ {
		e := s.FDT.entries[fd]
		if e != nil && e.handle != 0 {
			h.fs.Close(e.handle)
		}
	}
	h.cache.RemoveAll(sid)
	delete(h.shepherds, sid)
}

// handle dispatches a delegated syscall request to the appropriate handler.
func (h *syscallHandler) handle(req sys.SyscallRequest) {
	switch req.SysID {
	// --- Local-only syscalls ---
	case sysid.Close:
		h.sysClose(req)
	case sysid.Lseek:
		h.sysLseek(req)
	case sysid.Getcwd:
		h.sysGetcwd(req)
	case sysid.Chdir:
		h.sysChdir(req)
	case sysid.Fchdir:
		h.sysFchdir(req)
	case sysid.Ioctl:
		h.sysIoctl(req)
	case sysid.Fsync, sysid.Fdatasync:
		h.sysFsync(req)
	case sysid.Flock:
		h.sysFlock(req)

	// --- Metadata syscalls (via fs IPC) ---
	case sysid.Openat:
		h.sysOpenat(req)
	case sysid.Fstat:
		h.sysFstat(req)
	case sysid.Fstatat:
		h.sysFstatat(req)
	case sysid.Mkdirat:
		h.sysMkdirat(req)
	case sysid.Unlinkat:
		h.sysUnlinkat(req)
	case sysid.Renameat:
		h.sysRenameat(req)
	case sysid.Ftruncate:
		h.sysFtruncate(req)
	case sysid.Getdents64:
		h.sysGetdents64(req)
	case sysid.Faccessat:
		h.sysFaccessat(req)
	case sysid.Fchmodat:
		h.sysFchmodat(req)
	case sysid.Utimensat:
		h.sysUtimensat(req)
	case sysid.Readlinkat:
		h.sysReadlinkat(req)
	case sysid.Statfs:
		h.sysStatfs(req)
	case sysid.Fstatfs:
		h.sysFstatfs(req)

	// --- Data syscalls (via fs IPC) ---
	case sysid.Read:
		h.sysRead(req)
	case sysid.Write:
		h.sysWrite(req)
	case sysid.Writev:
		h.sysWritev(req)
	case sysid.Readv:
		h.sysReadv(req)
	case sysid.Pread64:
		h.sysPread64(req)
	case sysid.Pwrite64:
		h.sysPwrite64(req)
	case sysid.MmapPageFill:
		h.sysMmapPageFill(req)
	case sysid.MmapPageFlush:
		h.sysMmapPageFlush(req)

	default:
		req.Reply(ENOSYS)
	}
}

// ============================================================
// Local-only syscalls
// ============================================================

func (h *syscallHandler) sysClose(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindStdin || e.kind == fdKindStdout || e.kind == fdKindStderr {
		req.Reply(EOK)
		return
	}
	if e.writeBuf != nil && len(e.writeBuf) > 0 {
		h.flushWriteBuf(req.CallerPID, fd, e) // best-effort; ignore error on close
	}
	if e.handle != 0 {
		h.fs.Close(e.handle)
	}
	h.cache.RemoveFD(req.CallerPID, fd)
	fdt.free(fd)
	req.Reply(EOK)
}

func (h *syscallHandler) sysLseek(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	offset := int64(req.Args[1])
	whence := int(req.Args[2])

	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindStdin || e.kind == fdKindStdout || e.kind == fdKindStderr {
		req.Reply(ESPIPE)
		return
	}

	var newOff int64
	switch whence {
	case 0: // SEEK_SET
		newOff = offset
	case 1: // SEEK_CUR
		newOff = e.offset + offset
	case 2: // SEEK_END
		newOff = int64(e.size) + offset
	default:
		req.Reply(EINVAL)
		return
	}
	if newOff < 0 {
		req.Reply(EINVAL)
		return
	}

	e.offset = newOff
	req.Reply(newOff)
}

func (h *syscallHandler) sysGetcwd(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	cwd := fdt.cwd
	n := copy(buf, cwd)
	if n < len(buf) {
		buf[n] = 0
		n++
	}
	req.Reply(int64(n))
}

func (h *syscallHandler) sysChdir(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)

	isDir, _, err := h.fs.Resolve(absPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if !isDir {
		req.Reply(ENOTDIR)
		return
	}

	fdt.cwd = absPath
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchdir(req sys.SyscallRequest) {
	shep := h.getShepherd(req.CallerPID)
	fd := int(req.Args[0])
	e := shep.FDT.get(fd)
	if e == nil || e.kind == fdKindNone {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindDir {
		req.Reply(ENOTDIR)
		return
	}
	shep.FDT.cwd = e.path
	req.Reply(EOK)
}

func (h *syscallHandler) sysIoctl(req sys.SyscallRequest) {
	req.Reply(ENOTTY)
}

func (h *syscallHandler) sysFsync(req sys.SyscallRequest) {
	fd := int(req.Arg0())

	if fd >= 0 {
		fdt := h.getShepherd(req.CallerPID).FDT
		if e := fdt.get(fd); e != nil && e.writeBuf != nil && len(e.writeBuf) > 0 {
			if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
				req.Reply(int64(errToErrno(err)))
				return
			}
		}

		// Flush dirty cached pages for this fd to ext2 BEFORE syncing.
		// The pwrite fast path writes directly to cached pages without
		// touching ext2. Without this flush, fdatasync/fsync would leave
		// stale data on disk, causing mmap coherence failures after re-mmap
		// (bbolt reads zeros for pages it just wrote).
		h.cache.FlushAllPagesForFD(req.CallerPID, fd, func(offset int64, data []byte) (int, error) {
			return h.writePageToExt2(req.CallerPID, fd, offset, data)
		})
	}

	if err := h.fs.Sync(); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

// sysFlock implements flock(fd, operation).
// arg0 = fd, arg1 = operation (LOCK_SH, LOCK_EX, LOCK_UN, optionally | LOCK_NB).
func (h *syscallHandler) sysFlock(req sys.SyscallRequest) {
	fd := int(req.Args[0])
	op := int(req.Args[1])
	shep := h.getShepherd(req.CallerPID)
	e := shep.FDT.get(fd)
	if e == nil || e.kind == fdKindNone {
		req.Reply(EBADF)
		return
	}
	if e.handle == 0 {
		// stdio fds don't support locking.
		req.Reply(EINVAL)
		return
	}
	nonblock := (op & flockNB) != 0
	kind := op &^ flockNB
	switch kind {
	case flockSH, flockEX:
		req.Reply(h.flocks.acquire(e.handle, kind, nonblock, shep.Locks))
	case flockUN:
		req.Reply(h.flocks.release(e.handle, shep.Locks))
	default:
		req.Reply(EINVAL)
	}
}

// ============================================================
// Metadata syscalls (via fs IPC)
// ============================================================

func (h *syscallHandler) sysOpenat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	flags := int32(req.Args[2])
	mode := uint32(req.Args[3])

	handle, ftype, size, err := h.fs.Open(absPath, uint32(flags), mode)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	newFD := fdt.alloc(3)
	if newFD < 0 {
		h.fs.Close(handle)
		req.Reply(EMFILE)
		return
	}

	kind := fdKindFile
	if ftype == ftDir {
		kind = fdKindDir
	}

	entry := &fdEntry{
		kind:   kind,
		handle: handle,
		size:   size,
		ftype:  ftype,
		flags:  flags,
		path:   absPath,
	}
	if kind == fdKindFile {
		entry.writeBuf = []byte{} // non-nil = buffering active; zero-capacity, allocates on first write
		entry.writeBufOff = int64(size)
	}
	fdt.put(newFD, entry)
	req.Reply(int64(newFD))
}

func (h *syscallHandler) sysFstat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}

	if e.kind == fdKindStdin || e.kind == fdKindStdout || e.kind == fdKindStderr {
		fillStdioStatBuf(buf)
		req.Reply(128)
		return
	}

	fsErr, err := h.fs.Fstat(e.handle)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}

	copy(buf[:128], h.fs.DataSlice(128))
	req.Reply(128)
}

func (h *syscallHandler) sysFstatat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	fsErr, err := h.fs.Stat(absPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}

	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}
	copy(buf[:128], h.fs.DataSlice(128))
	req.Reply(128)
}

func (h *syscallHandler) sysMkdirat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.Mkdir(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUnlinkat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	flags := uint32(req.Args[2])

	// Linux distinguishes unlink (rejects directories) from rmdir (requires
	// a directory). ext2.Remove handles both, so we stat first and enforce
	// the AT_REMOVEDIR contract here. Without this Go's os.Remove cannot
	// detect that it should fall back to rmdir on a directory target.
	isDir, _, rerr := h.fs.Resolve(absPath)
	if rerr != nil {
		req.Reply(int64(errToErrno(rerr)))
		return
	}
	if (flags & AT_REMOVEDIR) != 0 {
		if !isDir {
			req.Reply(ENOTDIR)
			return
		}
	} else {
		if isDir {
			req.Reply(EISDIR)
			return
		}
	}
	if err := h.fs.Remove(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysRenameat(req sys.SyscallRequest) {
	// Data page contains "oldpath\0newpath\0". Arg0 = offset of newpath.
	d := req.Data()
	if d == nil {
		req.Reply(EINVAL)
		return
	}
	newOff := int(req.Arg0())
	if newOff <= 0 || newOff >= len(d) {
		req.Reply(EINVAL)
		return
	}
	// Extract old path (up to first null).
	var oldPath string
	for i := 0; i < newOff; i++ {
		if d[i] == 0 {
			oldPath = string(d[:i])
			break
		}
	}
	// Extract new path (from newOff to next null).
	var newPath string
	for i := newOff; i < len(d); i++ {
		if d[i] == 0 {
			newPath = string(d[newOff:i])
			break
		}
	}
	if oldPath == "" || newPath == "" {
		req.Reply(EINVAL)
		return
	}
	// Resolve relative paths through the caller's CWD.
	fdt := h.getShepherd(req.CallerPID).FDT
	oldPath = fdt.resolvePath(oldPath)
	newPath = fdt.resolvePath(newPath)

	err := h.fs.Rename(oldPath, newPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(0)
}

func (h *syscallHandler) sysFtruncate(req sys.SyscallRequest) {
	shep := h.getShepherd(req.CallerPID)
	fd := int(req.Args[0])
	newSize := int64(req.Args[1])
	e := shep.FDT.get(fd)
	if e == nil || e.kind == fdKindNone {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindFile {
		req.Reply(EINVAL)
		return
	}
	if err := h.fs.Truncate(e.handle, newSize); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Invalidate cached pages beyond the new size.
	h.cache.RemoveRange(req.CallerPID, fd, newSize, 1<<62)
	e.size = uint32(newSize)
	req.Reply(EOK)
}

func (h *syscallHandler) sysGetdents64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindDir {
		req.Reply(ENOTDIR)
		return
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}

	dataLen, entryCount, err := h.fs.ReadDir(e.handle, int(e.offset))
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	if dataLen == 0 {
		req.Reply(EOK) // no more entries
		return
	}
	n := dataLen
	if n > len(buf) {
		n = len(buf)
	}
	copy(buf[:n], h.fs.DataSlice(n))
	e.offset += int64(entryCount) // entries marshaled
	req.Reply(int64(n))
}

func (h *syscallHandler) sysFaccessat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.Access(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchmodat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.SetMode(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUtimensat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.resolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.SetTimes(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysReadlinkat(req sys.SyscallRequest) {
	req.Reply(EINVAL) // no symlinks
}

// sysStatfs handles statfs(path, buf). We don't have per-volume metadata
// (the fs shepherd exposes no capacity IPC), so on a valid path we return
// synthetic-but-reasonable values for a single global filesystem.
func (h *syscallHandler) sysStatfs(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
	if _, _, err := h.fs.Resolve(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 120 {
		req.Reply(EFAULT)
		return
	}
	fillSyntheticStatfs(buf[:120])
	req.Reply(120)
}

// sysFstatfs handles fstatfs(fd, buf). Same synthetic values as statfs; the
// fd just needs to be a live file or directory descriptor.
func (h *syscallHandler) sysFstatfs(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil || e.kind == fdKindNone {
		req.Reply(EBADF)
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 120 {
		req.Reply(EFAULT)
		return
	}
	fillSyntheticStatfs(buf[:120])
	req.Reply(120)
}

// fillSyntheticStatfs writes a 120-byte linux/arm64 + linux/amd64 struct
// statfs64 with synthetic values. All fields are 8-byte __fsword_t /
// fsblkcnt64_t on 64-bit Linux, so the layout is just 15 u64s.
//
// Layout (bytes 0..119):
//
//	 0:  f_type       = 0xEF53 (EXT2_SUPER_MAGIC — closest match to our ext2 ramdisk)
//	 8:  f_bsize      = 4096
//	16:  f_blocks     = 1<<20 (4 GiB total, synthetic)
//	24:  f_bfree      = 1<<19
//	32:  f_bavail     = 1<<19
//	40:  f_files      = 0
//	48:  f_ffree      = 0
//	56:  f_fsid       = 0 (two 32-bit zeros)
//	64:  f_namelen    = 255
//	72:  f_frsize     = 4096
//	80:  f_flags      = 0
//	88:  f_spare[4]   = 0
func fillSyntheticStatfs(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
	p := (*[15]uint64)(unsafe.Pointer(&buf[0]))
	p[0] = 0xEF53       // f_type
	p[1] = 4096         // f_bsize
	p[2] = 1 << 20      // f_blocks
	p[3] = 1 << 19      // f_bfree
	p[4] = 1 << 19      // f_bavail
	p[5] = 0            // f_files
	p[6] = 0            // f_ffree
	p[7] = 0            // f_fsid
	p[8] = 255          // f_namelen
	p[9] = 4096         // f_frsize
	p[10] = 0           // f_flags
	// p[11..14] — f_spare — already zero
}

// ============================================================
// Data syscalls (via fs IPC)
// ============================================================

// isStdinRead returns true if the request is a read on fd 0 (stdin).
// Used by the delegate handler to skip wrapping stdin reads with sidIncRef/sidDecRef
// since those have their own async refcount lifecycle.
func (h *syscallHandler) isStdinRead(req sys.SyscallRequest) bool {
	if req.SysID != sysid.Read {
		return false
	}
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	return e != nil && e.kind == fdKindStdin
}

func (h *syscallHandler) sysRead(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindStdin {
		// Queue the request for the stdin drainer (main goroutine).
		// Don't reply — the drainer will reply when input arrives.
		// sidIncRef here; sidDecRef happens in fulfillRead via stdinDecRefCh.
		sidIncRef(req.CallerPID)
		reqQueue.Enqueue(&readDataResponse{req: req, callerSID: req.CallerPID})
		return
	}
	if e.kind == fdKindStdout || e.kind == fdKindStderr {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindDir {
		req.Reply(EISDIR)
		return
	}
	if e.writeBuf != nil && len(e.writeBuf) > 0 {
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			req.Reply(int64(errToErrno(err)))
			return
		}
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}

	// Check page cache first — if all pages in range are cached, read
	// directly from cached pages (same physical pages as mmap'd memory).
	if entries := h.cache.LookupRange(req.CallerPID, fd, e.offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, e.offset, buf[:count], count, int64(e.size)); ok {
			e.offset += int64(n)
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.handle, e.offset, count)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	if n > 0 {
		copy(buf[:n], h.fs.DataSlice(n))
	}
	e.offset += int64(n)
	req.Reply(int64(n))
}

// flushWriteBuf drains e.writeBuf to ext2 in 4KB chunks (the IPC window size).
// Called from sysClose, sysFsync, and sysRead before any pass-through I/O.
func (h *syscallHandler) flushWriteBuf(pid int16, fd int, e *fdEntry) error {
	buf := e.writeBuf
	off := e.writeBufOff
	winSize := h.fs.DataLen()
	for len(buf) > 0 {
		chunk := buf
		if len(chunk) > winSize {
			chunk = chunk[:winSize]
		}
		n := h.fs.WriteData(chunk)
		written, err := h.fs.Write(e.handle, off, n)
		if err != nil {
			return err
		}
		// Keep cached pages coherent with what we just wrote.
		if entries := h.cache.LookupRange(pid, fd, off, written); len(entries) > 0 {
			updateCachedPages(entries, off, chunk[:written])
		}
		off += int64(written)
		buf = buf[written:]
	}
	// Reset to a non-nil zero-capacity slice: keeps "buffering enabled" sentinel
	// while releasing the backing pages to the GC with no pre-allocation.
	e.writeBuf = []byte{}
	e.writeBufOff = e.offset
	return nil
}

// writeBufMaxBytes is the maximum number of bytes held in a write buffer before
// a forced flush. Bounds memory usage and limits data-loss window.
const writeBufMaxBytes = 1 << 20 // 1 MB

// idleHintCount counts ProtoIdleFlushHint messages received.
// idleFlushCount counts buffers actually flushed by idle hints.
var idleHintCount, idleFlushCount uint64

// flushOneBuffer scans all shepherd FD tables and flushes the first non-empty
// write buffer it finds. Called from the idle-flush hint handler; processes
// at most one buffer per call so the delegate handler stays responsive.
func (h *syscallHandler) flushOneBuffer() {
	idleHintCount++
	if idleHintCount <= 3 || idleHintCount%1000 == 0 {
		fmt.Printf("[linux] idle hint #%d (flushes so far: %d)\n", idleHintCount, idleFlushCount)
	}
	for pid, shep := range h.shepherds {
		for fd, e := range shep.FDT.entries {
			if e != nil && e.writeBuf != nil && len(e.writeBuf) > 0 {
				n := len(e.writeBuf)
				h.flushWriteBuf(pid, fd, e) // ignore error; best-effort idle flush
				idleFlushCount++
				fmt.Printf("[linux] idle flush #%d: pid=%d fd=%d bytes=%d\n",
					idleFlushCount, pid, fd, n)
				return
			}
		}
	}
}

func (h *syscallHandler) sysWrite(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.get(fd)
	if e == nil || e.kind == fdKindStdout || e.kind == fdKindStderr {
		// stdout/stderr handled by the display path in startDelegateHandler.
		req.Reply(int64(len(data)))
		return
	}
	if e.kind == fdKindStdin {
		req.Reply(EBADF)
		return
	}
	if data == nil {
		req.Reply(ENOSYS)
		return
	}

	// Buffered path: accumulate sequential writes and defer IPC to fsync/close.
	if e.writeBuf != nil {
		bufEnd := e.writeBufOff + int64(len(e.writeBuf))
		if e.offset == bufEnd {
			// Contiguous — append and return immediately, no IPC to fs.
			e.writeBuf = append(e.writeBuf, data...)
			e.offset += int64(len(data))
			if uint32(e.offset) > e.size {
				e.size = uint32(e.offset)
			}
			// Force flush once the buffer reaches the size cap.
			if len(e.writeBuf) >= writeBufMaxBytes {
				if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
					req.Reply(int64(errToErrno(err)))
					return
				}
			}
			req.Reply(int64(len(data)))
			return
		}
		// Non-contiguous write (seek happened) — flush buffer first, then fall
		// through to the direct write path below.
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			req.Reply(int64(errToErrno(err)))
			return
		}
	}

	// Direct write path: send data to ext2 immediately.
	// Also used for the first chunk after a seek flushes the buffer.
	n := h.fs.WriteData(data)
	written, err := h.fs.Write(e.handle, e.offset, n)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Keep cached pages coherent with on-disk data.
	if entries := h.cache.LookupRange(req.CallerPID, fd, e.offset, written); len(entries) > 0 {
		updateCachedPages(entries, e.offset, data[:written])
	}

	e.offset += int64(written)
	if uint32(e.offset) > e.size {
		e.size = uint32(e.offset)
	}
	if e.writeBuf != nil {
		// Re-arm buffer starting at new position for future sequential writes.
		e.writeBufOff = e.offset
	}
	req.Reply(int64(written))
}

func (h *syscallHandler) sysWritev(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysReadv(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

// sysPread64 implements pread64(fd, buf, count, offset).
// Like read but uses the caller-supplied offset without changing the fd position.
func (h *syscallHandler) sysPread64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindFile {
		req.Reply(ESPIPE)
		return
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}
	offset := int64(req.Args[3])

	// Check page cache first — if all pages in range are cached, read
	// directly from cached pages (same physical pages as mmap'd memory).
	if entries := h.cache.LookupRange(req.CallerPID, fd, offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, offset, buf[:count], count, int64(e.size)); ok {
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.handle, offset, count)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if n > 0 {
		copy(buf[:n], h.fs.DataSlice(n))
	}
	req.Reply(int64(n))
}

// sysPwrite64 implements pwrite64(fd, buf, count, offset).
// Like write but uses the caller-supplied offset without changing the fd position.
func (h *syscallHandler) sysPwrite64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindFile {
		req.Reply(ESPIPE)
		return
	}
	if data == nil {
		req.Reply(EFAULT)
		return
	}

	offset := int64(req.Args[3])

	// Always write through ext2 first — this ensures the on-disk file is
	// up to date so that mmap page faults after re-mmap see the correct data.
	// The old "fast path" that bypassed ext2 broke mmap/pwrite coherence:
	// pwrite updated cached pages only, then bolt re-mmapped, new page faults
	// read from ext2 which still had zeros → bbolt panic.
	n := h.fs.WriteData(data)
	written, err := h.fs.Write(e.handle, offset, n)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Update any cached pages that overlap so mmap reads without
	// re-faulting also see the new data.
	if entries := h.cache.LookupRange(req.CallerPID, fd, offset, written); len(entries) > 0 {
		updateCachedPages(entries, offset, data[:written])
	}

	endPos := offset + int64(written)
	if uint32(endPos) > e.size {
		e.size = uint32(endPos)
	}
	req.Reply(int64(written))
}

// sysMmapPageFill handles kernel requests to fill a file-backed mmap page.
// The kernel allocates a physical frame and maps it into our address space,
// then sends this request so we read file data into it. The page stays mapped
// in both our address space and the faulting shepherd's — this provides page
// cache coherence so read/write through us see the same data as mmap'd memory.
func (h *syscallHandler) sysMmapPageFill(req sys.SyscallRequest) {
	// Args[0]=fd, Args[1]=fileOffset, Args[2]=count
	// CallerPID = shepherd that owns the fd
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Args[0])
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	// Flush any pending write buffer to ext2 before filling the page.
	// sysWrite buffers sequential writes in e.writeBuf without writing to ext2
	// immediately. If the caller wrote data then mmap'd the same fd, the page
	// fault arrives here before the buffer is flushed — ext2 would return zeros.
	if len(e.writeBuf) > 0 {
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			fmt.Printf("[mmap-fill] sid=%d fd=%d flush err: %v\n", req.CallerPID, fd, err)
		}
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}

	offset := int64(req.Args[1])
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}

	// Zero the page first — the kernel allocates physical frames from the
	// buddy allocator without zeroing, and if the file is shorter than a
	// full page (sparse/truncated), the tail would contain stale data.
	for i := range buf[:count] {
		buf[i] = 0
	}

	n, err := h.fs.Read(e.handle, offset, count)
	if err != nil {
		fmt.Printf("[mmap-fill] sid=%d fd=%d offset=%d READ ERR: %v\n", req.CallerPID, fd, offset, err)
		req.Reply(int64(errToErrno(err)))
		return
	}
	if n > 0 {
		copy(buf[:n], h.fs.DataSlice(n))
	}
	// Verbose mmap-fill trace disabled — UART saturation starved CPU-bound shepherds.

	// Record this page in the cache so read/pread/write/pwrite can
	// see mmap'd data without going through ext2.
	pageAlignedOffset := int64(req.Args[1]) &^ 0xFFF
	h.cache.Add(req.CallerPID, fd, pageAlignedOffset, req.DataVA())

	req.Reply(int64(n))
}

// sysMmapPageFlush handles kernel requests to flush dirty cached pages and
// return handler VAs for PTE cleanup. The kernel sends this on munmap or
// shepherd death. The handler:
//  1. Flushes dirty pages to ext2 (first round only — subsequent rounds
//     have no dirty pages left)
//  2. Removes up to 511 page cache entries
//  3. Writes their handler VAs into the response page
//  4. Replies with the number of VAs written
//
// The kernel reads the response page, unmaps those handler PTEs, and sends
// another round if count == 511.
//
// Args[0]=fd (0xFFFFFFFF = all fds, for death cleanup)
// Args[1]=callerSID
// DataVA/DataLen = response page (handler writes VAs here)
//
// Response page layout:
//
//	[0:4]    uint32   count (0..511)
//	[4:8]    uint32   reserved
//	[8:4096] [511]uint64  handler VAs to unmap
const maxFlushVAsPerRound = 511

func (h *syscallHandler) sysMmapPageFlush(req sys.SyscallRequest) {
	callerSID := int16(req.Args[1])
	fd := int(req.Args[0])
	allFDs := fd == 0xFFFFFFFF

	responseBuf := req.DataBuf()
	if responseBuf == nil || len(responseBuf) < 4096 {
		req.Reply(EINVAL)
		return
	}

	// Flush all pages to ext2. We flush everything (not just pages marked
	// dirty via syscalls) because user writes through the mmap VA don't
	// generate syscalls, so we have no way to track which pages changed.
	if allFDs {
		h.cache.FlushAllPagesForSID(callerSID, func(cacheFD int, offset int64, data []byte) (int, error) {
			return h.writePageToExt2(callerSID, cacheFD, offset, data)
		})
	} else {
		h.cache.FlushAllPagesForFD(callerSID, fd, func(offset int64, data []byte) (int, error) {
			return h.writePageToExt2(callerSID, fd, offset, data)
		})
	}

	// Remove up to 511 entries and write their VAs to the response page.
	var removed []pageCacheEntry
	if allFDs {
		removed = h.cache.RemoveAllBatch(callerSID, maxFlushVAsPerRound)
	} else {
		removed = h.cache.RemoveRangeBatch(callerSID, fd, maxFlushVAsPerRound)
	}

	// Write response: count + VA array
	count := uint32(len(removed))
	*(*uint32)(unsafe.Pointer(&responseBuf[0])) = count
	*(*uint32)(unsafe.Pointer(&responseBuf[4])) = 0 // reserved

	for i, entry := range removed {
		offset := 8 + i*8
		*(*uint64)(unsafe.Pointer(&responseBuf[offset])) = uint64(entry.VA)
	}

	// Verbose mmap-flush trace disabled — UART saturation.
	req.Reply(int64(count))
}

// writePageToExt2 writes a single page to ext2 for the given (sid, fd, offset).
func (h *syscallHandler) writePageToExt2(sid int16, fd int, offset int64, data []byte) (int, error) {
	fdt := h.getShepherd(sid).FDT
	e := fdt.get(fd)
	if e == nil || e.kind != fdKindFile {
		return 0, nil // skip non-file fds
	}
	n := h.fs.WriteData(data)
	written, err := h.fs.Write(e.handle, offset, n)
	if err != nil {
		return 0, err
	}
	endPos := offset + int64(written)
	if uint32(endPos) > e.size {
		e.size = uint32(endPos)
	}
	return written, nil
}

// ============================================================
// Helpers
// ============================================================

// fillStdioStatBuf writes a minimal stat struct for stdin/stdout/stderr.
// Arch-specific: see stat_arm64.go / stat_amd64.go / stat_riscv64.go.

// errToErrno converts an fsclient error to a negative errno value.
func errToErrno(err error) int32 {
	if err == nil {
		return 0
	}
	if errno, ok := fsclient.IsErrno(err); ok {
		return errno
	}
	return -5 // EIO
}
