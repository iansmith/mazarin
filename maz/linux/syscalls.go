package main

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"mazzy/maz/linux/internal/fdtable"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/sys"
	"mazzy/shared/dlist"
	"mazzy/shared/sysid"
)

// isZapPath returns true for bleve scorch *.zap segment files.
// Diagnostic-only: gates the [lin:openat .zap] traces so we can
// compare resolved paths between create and reopen of the same
// segment (path-resolution-divergence hypothesis).
func isZapPath(p string) bool {
	return strings.HasSuffix(p, ".zap")
}

// syscallHandler processes delegated file syscalls for the linux shepherd.
// Per-shepherd filesystem state (FD tables, flocks, CWD) is tracked in
// ShepherdFilesystemData, keyed by caller SID.
//
// mu protects the cross-shepherd maps (shepherds, orphanHandles). Per-shepherd
// state is locked separately via ShepherdFilesystemData.mu, which is the lock
// held for the lifetime of a handler. flocks and cache have their own
// internal locking.
type syscallHandler struct {
	mu            sync.Mutex
	shepherds     map[int16]*ShepherdFilesystemData
	flocks        *flockTable
	fs            fsclient.FSClient
	cache         *pageCache

	// orphanHandles tracks fs handles whose owning fd has been closed
	// but whose cached mmap pages still need a writeback path. Linux
	// semantics: mappings survive close, so the handle must too. Keyed
	// by sid → inum. Drained in sysMmapPageFlush after the last page
	// for an inum is removed, and on shepherd death.
	orphanHandles map[int16]map[uint32]uint32
}

func newSyscallHandler(fs fsclient.FSClient) *syscallHandler {
	return &syscallHandler{
		shepherds:     make(map[int16]*ShepherdFilesystemData),
		flocks:        newFlockTable(),
		fs:            fs,
		cache:         newPageCache(),
		orphanHandles: make(map[int16]map[uint32]uint32),
	}
}

// markOrphanHandle records that fs handle is being kept alive past sysClose
// because cache pages for inum still reference it.
func (h *syscallHandler) markOrphanHandle(sid int16, inum, handle uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		m = make(map[uint32]uint32)
		h.orphanHandles[sid] = m
	}
	m[inum] = handle
}

// closeOrphanHandleIfDrained closes the orphan fs handle for (sid, inum) if
// the cache no longer has pages for it. Called after RemoveBatch rounds.
func (h *syscallHandler) closeOrphanHandleIfDrained(sid int16, inum uint32) {
	h.mu.Lock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		h.mu.Unlock()
		return
	}
	handle, ok := m[inum]
	if !ok {
		h.mu.Unlock()
		return
	}
	if h.cache.HasPagesFor(sid, inum) {
		h.mu.Unlock()
		return
	}
	delete(m, inum)
	if len(m) == 0 {
		delete(h.orphanHandles, sid)
	}
	h.mu.Unlock()
	// Close happens outside the lock — fsclient is self-locking and we
	// must not hold h.mu across an IPC round-trip.
	_ = h.fs.Close(handle)
}

// closeAllOrphanHandlesForSID closes every orphan handle for sid. Used in
// shepherd death cleanup after the cache is fully drained.
func (h *syscallHandler) closeAllOrphanHandlesForSID(sid int16) {
	h.mu.Lock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		h.mu.Unlock()
		return
	}
	handles := make([]uint32, 0, len(m))
	for _, handle := range m {
		handles = append(handles, handle)
	}
	delete(h.orphanHandles, sid)
	h.mu.Unlock()
	// Close handles outside the lock.
	for _, handle := range handles {
		_ = h.fs.Close(handle)
	}
}

// getShepherd returns the per-shepherd filesystem state for the given SID,
// creating it lazily on first contact. The returned pointer is stable for
// the lifetime of the shepherd; callers acquire .mu on it for the duration
// of a handler.
func (h *syscallHandler) getShepherd(sid int16) *ShepherdFilesystemData {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.shepherds[sid]
	if s == nil {
		s = &ShepherdFilesystemData{
			SID:   sid,
			FDT:   fdtable.New(),
			Locks: dlist.New[*flockEntry](),
		}
		h.shepherds[sid] = s
	}
	return s
}

// cleanupShepherd closes all open FDs, releases all flocks, and removes
// the per-shepherd state for the given SID. Called on shepherd death.
func (h *syscallHandler) cleanupShepherd(sid int16) {
	h.mu.Lock()
	s := h.shepherds[sid]
	if s == nil {
		h.mu.Unlock()
		return
	}
	delete(h.shepherds, sid)
	h.mu.Unlock()
	// Take per-shepherd lock so any in-flight handler for this shepherd
	// (already holding s.mu) drains before we touch its FDT.
	s.mu.Lock()
	// Snapshot fs handles to close outside the lock. Stdio entries (fd 0/1/2)
	// carry Handle == 0, so the filter below skips them.
	handles := make([]uint32, 0, 8)
	s.FDT.Each(func(_ int, e *fdtable.Entry) bool {
		if e.Handle != 0 {
			handles = append(handles, e.Handle)
		}
		return true
	})
	// Release all advisory locks.
	h.flocks.releaseAll(s.Locks)
	s.mu.Unlock()
	// Close fs handles outside per-shepherd lock — these are IPC calls.
	for _, handle := range handles {
		h.fs.Close(handle)
	}
	// Close any handles that were kept alive past close() because cached
	// mmap pages were still live. The kernel-driven death-cleanup path
	// (sysMmapPageFlush with fd=allFDs) already ran before this hook, so
	// the cache is fully drained — but the orphan-handle table tracks
	// handles independent of the cache.
	h.closeAllOrphanHandlesForSID(sid)
	h.cache.RemoveAll(sid)
}

// handle dispatches a delegated syscall request to the appropriate handler.
//
// Per-shepherd serialization: the caller's ShepherdFilesystemData.mu is held
// for the lifetime of the dispatch so that one shepherd's syscalls remain
// ordered with respect to each other (matching the historical single-goroutine
// behavior). Different shepherds run on different goroutines and don't block
// on each other. The lock is never held across a fsclient call's response
// wait — fsclient has its own internal mutex that already serializes the
// underlying IPC.
func (h *syscallHandler) handle(req sys.SyscallRequest) {
	shep := h.getShepherd(req.CallerPID)
	shep.mu.Lock()
	defer shep.mu.Unlock()
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
	case sysid.Dup3:
		h.sysDup3(req)
	case sysid.Fcntl:
		h.sysFcntl(req)

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
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(EOK)
		return
	}
	h.releaseFDResources(req.CallerPID, fd, e)
	fdt.Free(fd)
	req.Reply(EOK)
}

// releaseFDResources runs the close-time disposition for a single open file
// entry — flushing any pending write buffer and releasing (or orphaning) the
// fs-side handle — WITHOUT freeing the table slot. sysClose calls it then
// frees the slot; sysDup3 calls it for the entry it is about to overwrite at
// newfd. Stdio entries carry no handle/WriteBuf, so callers gate on Kind
// before invoking it.
func (h *syscallHandler) releaseFDResources(sid int16, fd int, e *fdtable.Entry) {
	if e.WriteBuf != nil && len(e.WriteBuf) > 0 {
		h.flushWriteBuf(sid, fd, e) // best-effort; ignore error on close
	}
	// Linux semantics: mmap'd pages survive close. If the cache still has
	// pages for this inode, leave the fs handle open so the eventual
	// munmap (or shepherd death) can flush dirty pages back to disk via
	// that handle. The page cache stores its own copy of `handle` in
	// every entry, so even after fdt.Free() the writeback path keeps
	// working. The handle is released by closeOrphanHandleIfDrained when
	// the last page for this inum is removed, or by death cleanup.
	if e.Handle != 0 {
		if h.cache.HasPagesFor(sid, e.Inum) {
			h.markOrphanHandle(sid, e.Inum, e.Handle)
		} else {
			h.fs.Close(e.Handle)
		}
	}
}

// sysDup3 implements dup3(oldfd, newfd, flags): newfd is made to refer to the
// same open file as oldfd, after closing whatever previously occupied newfd.
// EINVAL when oldfd == newfd, EBADF on a closed oldfd or out-of-range newfd.
// The displaced newfd is disposed exactly like sysClose via releaseFDResources.
func (h *syscallHandler) sysDup3(req sys.SyscallRequest) {
	sid := req.CallerPID
	fdt := h.getShepherd(sid).FDT
	oldfd := int(req.Args[0])
	newfd := int(req.Args[1])
	flags := int32(req.Args[2])

	_, errno := fdt.Dup3(oldfd, newfd, flags, func(displaced *fdtable.Entry) {
		// Give the displaced entry the same close-time disposition sysClose
		// would (WriteBuf flush + orphan/release of any fs handle).
		h.releaseFDResources(sid, newfd, displaced)
	})
	req.Reply(int64(errno))
}

// fcntl command numbers (identical on ARM64 and amd64).
const (
	fSETFD    = 2 // F_SETFD — set fd flags (only FD_CLOEXEC is meaningful)
	fGETFD    = 1 // F_GETFD — get fd flags
	fGETFL    = 3 // F_GETFL — get file status flags (access mode)
	fSETFL    = 4 // F_SETFL — set file status flags
	fdCLOEXEC = 1 // FD_CLOEXEC bit returned/accepted by F_GETFD/F_SETFD
)

// sysFcntl implements the fcntl commands the shepherd path owns: F_SETFD /
// F_GETFD store and read the per-fd close-on-exec bit (MAZ-119), and the stdio
// fast paths (fd 0/1/2) for F_GETFD/F_SETFD/F_GETFL/F_SETFL mirror the kernel's
// in-kernel SyscallFcntl stub so the Go runtime's os.init() probes on
// stdin/stdout/stderr keep working once fcntl is delegated here. Any other
// command returns ENOSYS, matching the kernel stub for fd > 2.
func (h *syscallHandler) sysFcntl(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Args[0])
	cmd := req.Args[1]
	arg := req.Args[2]

	e := fdt.Get(fd)

	switch cmd {
	case fGETFD:
		if e == nil {
			req.Reply(EBADF)
			return
		}
		if e.Cloexec {
			req.Reply(fdCLOEXEC)
		} else {
			req.Reply(EOK) // stdio defaults to no flags, matching the kernel stub
		}
	case fSETFD:
		if e == nil {
			req.Reply(EBADF)
			return
		}
		e.Cloexec = arg&fdCLOEXEC != 0
		req.Reply(EOK)
	case fGETFL:
		// Access mode for the stdio fds, mirroring the kernel stub: stdin is
		// read-only, stdout/stderr are write-only.
		if e == nil {
			req.Reply(EBADF)
			return
		}
		if e.Kind == fdtable.KindStdin {
			req.Reply(0) // O_RDONLY
			return
		}
		if e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
			req.Reply(1) // O_WRONLY
			return
		}
		req.Reply(int64(e.Flags))
	case fSETFL:
		// Status-flag changes are a no-op here (and the kernel stub pretends
		// to set them for stdio); only a closed fd is an error.
		if e == nil {
			req.Reply(EBADF)
			return
		}
		req.Reply(EOK)
	default:
		req.Reply(ENOSYS)
	}
}

func (h *syscallHandler) sysLseek(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	offset := int64(req.Args[1])
	whence := int(req.Args[2])

	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(ESPIPE)
		return
	}

	var newOff int64
	switch whence {
	case 0: // SEEK_SET
		newOff = offset
	case 1: // SEEK_CUR
		newOff = e.Offset + offset
	case 2: // SEEK_END
		newOff = int64(e.Size) + offset
	default:
		req.Reply(EINVAL)
		return
	}
	if newOff < 0 {
		req.Reply(EINVAL)
		return
	}

	e.Offset = newOff
	req.Reply(newOff)
}

func (h *syscallHandler) sysGetcwd(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	cwd := fdt.Cwd
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
	absPath := fdt.ResolvePath(path)

	isDir, _, err := h.fs.Resolve(absPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if !isDir {
		req.Reply(ENOTDIR)
		return
	}

	fdt.Cwd = absPath
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchdir(req sys.SyscallRequest) {
	shep := h.getShepherd(req.CallerPID)
	fd := int(req.Args[0])
	e := shep.FDT.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindDir {
		req.Reply(ENOTDIR)
		return
	}
	shep.FDT.Cwd = e.Path
	req.Reply(EOK)
}

func (h *syscallHandler) sysIoctl(req sys.SyscallRequest) {
	req.Reply(ENOTTY)
}

func (h *syscallHandler) sysFsync(req sys.SyscallRequest) {
	fd := int(req.Arg0())

	if fd >= 0 {
		fdt := h.getShepherd(req.CallerPID).FDT
		if e := fdt.Get(fd); e != nil && e.WriteBuf != nil && len(e.WriteBuf) > 0 {
			if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
				req.Reply(int64(errToErrno(err)))
				return
			}
		}

		// Flush dirty cached pages for this inode to ext2 BEFORE syncing.
		// The pwrite fast path writes directly to cached pages without
		// touching ext2. Without this flush, fdatasync/fsync would leave
		// stale data on disk, causing mmap coherence failures after re-mmap
		// (bbolt reads zeros for pages it just wrote).
		if e := fdt.Get(fd); e != nil {
			h.cache.FlushAllPagesForInum(req.CallerPID, e.Inum, func(handle uint32, offset int64, data []byte) (int, error) {
				return h.writePageByHandle(req.CallerPID, handle, offset, data)
			})
		}
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
	e := shep.FDT.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Handle == 0 {
		// stdio fds don't support locking.
		req.Reply(EINVAL)
		return
	}
	nonblock := (op & flockNB) != 0
	kind := op &^ flockNB
	switch kind {
	case flockSH, flockEX:
		req.Reply(h.flocks.acquire(e.Handle, kind, nonblock, shep.Locks))
	case flockUN:
		req.Reply(h.flocks.release(e.Handle, shep.Locks))
	default:
		req.Reply(EINVAL)
	}
}

// ============================================================
// Metadata syscalls (via fs IPC)
// ============================================================

func (h *syscallHandler) sysOpenat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	rawPath := req.PathString()
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), rawPath)
	if e != 0 {
		if isZapPath(rawPath) || isZapPath(absPath) {
			fmt.Printf("[lin:openat .zap RESOLVEAT FAIL] dirfd=%d raw=%q err=%d\n",
				int32(req.Args[0]), rawPath, e)
		}
		req.Reply(e)
		return
	}
	flags := int32(req.Args[2])
	mode := uint32(req.Args[3])

	handle, inum, ftype, size, err := h.fs.Open(absPath, uint32(flags), mode)
	if err != nil {
		if isZapPath(absPath) {
			fmt.Printf("[lin:openat .zap FAIL] abspath=%q flags=0x%x mode=0%o err=%v errno=%d\n",
				absPath, uint32(flags), mode, err, errToErrno(err))
		}
		req.Reply(int64(errToErrno(err)))
		return
	}

	newFD := fdt.Alloc(3)
	if newFD < 0 {
		h.fs.Close(handle)
		req.Reply(EMFILE)
		return
	}

	kind := fdtable.KindFile
	if ftype == fdtable.FtDir {
		kind = fdtable.KindDir
	}

	entry := &fdtable.Entry{
		Kind:    kind,
		Handle:  handle,
		Inum:    inum,
		Size:    size,
		Ftype:   ftype,
		Flags:   flags,
		Path:    absPath,
		Cloexec: fdtable.CloexecFromFlags(flags),
	}
	if kind == fdtable.KindFile {
		entry.WriteBuf = []byte{} // non-nil = buffering active; zero-capacity, allocates on first write
		entry.WriteBufOff = int64(size)
	}
	fdt.Put(newFD, entry)
	req.Reply(int64(newFD))
}

func (h *syscallHandler) sysFstat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}

	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		fillStdioStatBuf(buf)
		req.Reply(128)
		return
	}

	fsErr, err := h.fs.Fstat(e.Handle, buf[:128])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}
	req.Reply(128)
}

func (h *syscallHandler) sysFstatat(req sys.SyscallRequest) {
	path := req.PathString()
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), path)
	if e != 0 {
		req.Reply(e)
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}
	fsErr, err := h.fs.Stat(absPath, buf[:128])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}
	req.Reply(128)
}

func (h *syscallHandler) sysMkdirat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
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
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	flags := uint32(req.Args[2])

	// Linux distinguishes unlink (rejects directories) from rmdir (requires
	// a directory). ext2.Remove handles both, so we stat first and enforce
	// the fdtable.AT_REMOVEDIR contract here. Without this Go's os.Remove cannot
	// detect that it should fall back to rmdir on a directory target.
	isDir, _, rerr := h.fs.Resolve(absPath)
	if rerr != nil {
		req.Reply(int64(errToErrno(rerr)))
		return
	}
	if (flags & fdtable.AT_REMOVEDIR) != 0 {
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
	oldPath = fdt.ResolvePath(oldPath)
	newPath = fdt.ResolvePath(newPath)

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
	e := shep.FDT.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
		req.Reply(EINVAL)
		return
	}
	if err := h.fs.Truncate(e.Handle, newSize); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Invalidate cached pages beyond the new size.
	// KNOWN LEAK: the returned entries' handler-side PTEs and physical pages
	// are NOT torn down here — the kernel doesn't know to release them. They
	// persist until the next munmap of this fd or shepherd death (whichever
	// comes first). Proper fix needs a kernel-side "drop these VAs" IPC; for
	// now we log so we can see how often this leaks in practice.
	leaked := h.cache.RemoveRange(req.CallerPID, e.Inum, newSize, 1<<62)
	if len(leaked) > 0 {
		fmt.Printf("[ftruncate:LEAK] sid=%d fd=%d inum=%d newSize=%d leaked=%d cache entries (handler PTEs orphaned until munmap/death)\n",
			req.CallerPID, fd, e.Inum, newSize, len(leaked))
	}
	e.Size = uint32(newSize)
	req.Reply(EOK)
}

func (h *syscallHandler) sysGetdents64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindDir {
		req.Reply(ENOTDIR)
		return
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}

	// fs.maz packs as many dirents as fit in its shared data window, so
	// we need a scratch buffer big enough to receive the full response.
	// Allocate per-call (getdents64 is rare relative to read/write).
	scratch := make([]byte, h.fs.DataLen())
	dataLen, _, err := h.fs.ReadDir(e.Handle, int(e.Offset), scratch)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	if dataLen == 0 {
		req.Reply(EOK) // no more entries
		return
	}
	src := scratch[:dataLen]

	// fs.maz packs as many dirents as fit in its 65KB shared data window;
	// the user's buffer is typically 4KB. We must NOT advance e.Offset by
	// entryCount when the user buffer can't hold all of them — doing so
	// silently drops the dirents that didn't fit, breaking filepath.Walk
	// on directories with more than ~80 entries (Bug A in findings.md).
	//
	// Instead: walk the dirent records inside the truncated copy, count
	// how many actually fit, and advance offset by that count. The next
	// getdents64 call will pick up at the first dropped entry.
	delivered := deliveredDirents(src, len(buf))
	n := delivered.bytes
	if n > 0 {
		copy(buf[:n], src[:n])
	}
	e.Offset += int64(delivered.count)
	req.Reply(int64(n))
}

// deliveredDirents walks the linux_dirent64 records in src and returns
// how many full records fit inside maxBytes (rounded down to a record
// boundary), plus the byte length consumed by those records. Each
// record's reclen lives at bytes [16:18] of its header.
func deliveredDirents(src []byte, maxBytes int) struct {
	bytes int
	count int
} {
	var out struct {
		bytes int
		count int
	}
	off := 0
	for off+18 <= len(src) {
		// reclen is at offset 16 within the record (after ino[8] + offset[8]).
		reclen := int(uint16(src[off+16]) | uint16(src[off+17])<<8)
		if reclen <= 0 || off+reclen > len(src) {
			break // malformed or runs past the buffer
		}
		if off+reclen > maxBytes {
			break // doesn't fit in user buf
		}
		off += reclen
		out.count++
	}
	out.bytes = off
	return out
}

func (h *syscallHandler) sysFaccessat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
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
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
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
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
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
	absPath := fdt.ResolvePath(path)
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
	e := fdt.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
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
	e := fdt.Get(fd)
	return e != nil && e.Kind == fdtable.KindStdin
}

func (h *syscallHandler) sysRead(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin {
		// Queue the request for the stdin drainer (main goroutine).
		// Don't reply — the drainer will reply when input arrives.
		// sidIncRef here; sidDecRef happens in fulfillRead via stdinDecRefCh.
		sidIncRef(req.CallerPID)
		reqQueue.Enqueue(&readDataResponse{req: req, callerSID: req.CallerPID})
		return
	}
	if e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindDir {
		req.Reply(EISDIR)
		return
	}
	if e.WriteBuf != nil && len(e.WriteBuf) > 0 {
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
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, e.Offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, e.Offset, buf[:count], count, int64(e.Size)); ok {
			e.Offset += int64(n)
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.Handle, e.Offset, buf[:count])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	e.Offset += int64(n)
	req.Reply(int64(n))
}

// flushWriteBuf drains e.WriteBuf to ext2 in 4KB chunks (the IPC window size).
// Called from sysClose, sysFsync, and sysRead before any pass-through I/O.
func (h *syscallHandler) flushWriteBuf(pid int16, fd int, e *fdtable.Entry) error {
	buf := e.WriteBuf
	off := e.WriteBufOff
	winSize := h.fs.DataLen()
	for len(buf) > 0 {
		chunk := buf
		if len(chunk) > winSize {
			chunk = chunk[:winSize]
		}
		written, err := h.fs.Write(e.Handle, off, chunk)
		if err != nil {
			return err
		}
		// Keep cached pages coherent with what we just wrote.
		if entries := h.cache.LookupRange(pid, e.Inum, off, written); len(entries) > 0 {
			updateCachedPages(entries, off, chunk[:written])
		}
		off += int64(written)
		buf = buf[written:]
	}
	// Reset to a non-nil zero-capacity slice: keeps "buffering enabled" sentinel
	// while releasing the backing pages to the GC with no pre-allocation.
	e.WriteBuf = []byte{}
	e.WriteBufOff = e.Offset
	return nil
}

// writeBufMaxBytes is the maximum number of bytes held in a write buffer before
// a forced flush. Bounds memory usage and limits data-loss window.
const writeBufMaxBytes = 1 << 20 // 1 MB

// flushOneBuffer scans all shepherd FD tables and flushes the first non-empty
// write buffer it finds. Called from the idle-flush hint handler; processes
// at most one buffer per call so the delegate handler stays responsive.
func (h *syscallHandler) flushOneBuffer() {
	for pid, shep := range h.shepherds {
		flushed := false
		shep.FDT.Each(func(fd int, e *fdtable.Entry) bool {
			if len(e.WriteBuf) == 0 {
				return true // keep scanning
			}
			h.flushWriteBuf(pid, fd, e) // ignore error; best-effort idle flush
			flushed = true
			return false // processed one buffer; stop
		})
		if flushed {
			return
		}
	}
}

func (h *syscallHandler) sysWrite(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.Get(fd)
	if e == nil || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		// stdout/stderr handled by the display path in startDelegateHandler.
		req.Reply(int64(len(data)))
		return
	}
	if e.Kind == fdtable.KindStdin {
		req.Reply(EBADF)
		return
	}
	if data == nil {
		req.Reply(ENOSYS)
		return
	}

	// Buffered path: accumulate sequential writes and defer IPC to fsync/close.
	if e.WriteBuf != nil {
		bufEnd := e.WriteBufOff + int64(len(e.WriteBuf))
		if e.Offset == bufEnd {
			// Contiguous — append and return immediately, no IPC to fs.
			e.WriteBuf = append(e.WriteBuf, data...)
			e.Offset += int64(len(data))
			if uint32(e.Offset) > e.Size {
				e.Size = uint32(e.Offset)
			}
			// Force flush once the buffer reaches the size cap.
			if len(e.WriteBuf) >= writeBufMaxBytes {
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
	written, err := h.fs.Write(e.Handle, e.Offset, data)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Keep cached pages coherent with on-disk data.
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, e.Offset, written); len(entries) > 0 {
		updateCachedPages(entries, e.Offset, data[:written])
	}

	e.Offset += int64(written)
	if uint32(e.Offset) > e.Size {
		e.Size = uint32(e.Offset)
	}
	if e.WriteBuf != nil {
		// Re-arm buffer starting at new position for future sequential writes.
		e.WriteBufOff = e.Offset
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
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
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
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, offset, buf[:count], count, int64(e.Size)); ok {
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.Handle, offset, buf[:count])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(int64(n))
}

// sysPwrite64 implements pwrite64(fd, buf, count, offset).
// Like write but uses the caller-supplied offset without changing the fd position.
func (h *syscallHandler) sysPwrite64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
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
	written, err := h.fs.Write(e.Handle, offset, data)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Update any cached pages that overlap so mmap reads without
	// re-faulting also see the new data.
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, offset, written); len(entries) > 0 {
		updateCachedPages(entries, offset, data[:written])
	}

	endPos := offset + int64(written)
	if uint32(endPos) > e.Size {
		e.Size = uint32(endPos)
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
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	// Flush any pending write buffer to ext2 before filling the page.
	// sysWrite buffers sequential writes in e.WriteBuf without writing to ext2
	// immediately. If the caller wrote data then mmap'd the same fd, the page
	// fault arrives here before the buffer is flushed — ext2 would return zeros.
	if len(e.WriteBuf) > 0 {
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

	n, err := h.fs.Read(e.Handle, offset, buf[:count])
	if err != nil {
		fmt.Printf("[mmap-fill] sid=%d fd=%d offset=%d READ ERR: %v\n", req.CallerPID, fd, offset, err)
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Verbose mmap-fill trace disabled — UART saturation starved CPU-bound shepherds.

	// Record this page in the cache so read/pread/write/pwrite can
	// see mmap'd data without going through ext2. Keyed by inode (not
	// fd) so the page survives close per Linux semantics.
	pageAlignedOffset := int64(req.Args[1]) &^ 0xFFF
	h.cache.Add(req.CallerPID, e.Inum, pageAlignedOffset, req.DataVA(), e.Handle)

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
	// Args[2]/Args[3] = file-offset range to constrain the flush+remove.
	// Length == 0 means "all pages for the inode" (death cleanup or
	// legacy callers). For partial munmap the kernel passes the unmapped
	// VA range translated to file offsets so pages outside it stay live.
	startOffset := int64(req.Args[2])
	rangeLen := int64(req.Args[3])
	rangeBound := rangeLen > 0

	responseBuf := req.DataBuf()
	if responseBuf == nil || len(responseBuf) < 4096 {
		req.Reply(EINVAL)
		return
	}

	// The kernel sends fd from its file-mapping metadata. To address the
	// page cache (now inode-keyed) we resolve fd→inum via FDT. If the fd
	// has already been freed by sysClose (close-before-munmap, atypical
	// for bbolt/scorch but possible in pathological orders), we fall
	// back to the all-fds drain for this sid — better than leaking.
	var inum uint32
	inumKnown := false
	if !allFDs {
		fdt := h.getShepherd(callerSID).FDT
		if e := fdt.Get(fd); e != nil {
			inum = e.Inum
			inumKnown = true
		}
	}

	// Flush all pages to ext2. We flush everything (not just pages marked
	// dirty via syscalls) because user writes through the mmap VA don't
	// generate syscalls, so we have no way to track which pages changed.
	// Range-bounded flush is an optimization; flushing the whole inode is
	// always safe (just extra IO).
	flushByHandle := func(handle uint32, offset int64, data []byte) (int, error) {
		return h.writePageByHandle(callerSID, handle, offset, data)
	}
	if allFDs || !inumKnown {
		if !allFDs {
			inums := h.cache.InumsFor(callerSID)
			fmt.Printf("[pageCache:FALLBACK_ALLFDS] sid=%d fd=%d inumCount=%d (fd not in fdt, draining all)\n", callerSID, fd, len(inums))
			for _, inum := range inums {
				fmt.Printf("[pageCache:DRAIN] sid=%d inum=%d\n", callerSID, inum)
			}
		}
		h.cache.FlushAllPagesForSID(callerSID, flushByHandle)
	} else {
		h.cache.FlushAllPagesForInum(callerSID, inum, flushByHandle)
	}

	// Remove up to 511 entries and write their VAs to the response page.
	// Range-bounded removal is critical: a partial munmap must NOT drop
	// cache entries for pages still mapped in the caller's address space.
	var removed []pageCacheEntry
	if allFDs || !inumKnown {
		removed = h.cache.RemoveAllBatch(callerSID, maxFlushVAsPerRound)
	} else if rangeBound {
		removed = h.cache.RemoveRangeOffsetBatch(callerSID, inum, startOffset, rangeLen, maxFlushVAsPerRound)
		h.closeOrphanHandleIfDrained(callerSID, inum)
	} else {
		removed = h.cache.RemoveRangeBatch(callerSID, inum, maxFlushVAsPerRound)
		// If this round drained the last pages for the inum, close the
		// fs handle that was kept alive past sysClose for writeback.
		h.closeOrphanHandleIfDrained(callerSID, inum)
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
	e := fdt.Get(fd)
	if e == nil || e.Kind != fdtable.KindFile {
		return 0, nil // skip non-file fds
	}
	written, err := h.fs.Write(e.Handle, offset, data)
	if err != nil {
		return 0, err
	}
	endPos := offset + int64(written)
	if uint32(endPos) > e.Size {
		e.Size = uint32(endPos)
	}
	return written, nil
}

// writePageByHandle writes a single page to ext2 using a stored fs handle
// directly, bypassing the FDT. Used by mmap flush paths because the cache
// is keyed by inode and stores its own copy of the handle — the original
// fd may have been closed (Linux semantics: mmap survives close) and the
// FDT lookup would fail. The fs handle stays alive across close so long
// as cache pages reference it (sysClose checks HasPagesFor).
func (h *syscallHandler) writePageByHandle(sid int16, handle uint32, offset int64, data []byte) (int, error) {
	if handle == 0 {
		return 0, nil
	}
	written, err := h.fs.Write(handle, offset, data)
	if err != nil {
		return 0, err
	}
	// We don't update e.Size here — if the fd is still open and the size
	// changed, a subsequent fstat will go through to ext2 which returns
	// the authoritative size. Keeping e.Size stale is harmless because
	// only the original fd's reads use it, and they typically don't
	// straddle the just-flushed mmap region.
	_ = sid
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
