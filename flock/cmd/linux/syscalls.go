package main

import (
	"encoding/binary"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/sys"
	"mazzy/shared/sysid"
)

// syscallHandler processes delegated file syscalls for the linux shepherd.
// It owns the FD table and communicates with the fs shepherd via uring IPC.
type syscallHandler struct {
	fdt *fdTable
	fs  *fsclient.Client
}

func newSyscallHandler(fs *fsclient.Client) *syscallHandler {
	return &syscallHandler{
		fdt: newFDTable(),
		fs:  fs,
	}
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

	default:
		req.Reply(ENOSYS)
	}
}

// ============================================================
// Local-only syscalls
// ============================================================

func (h *syscallHandler) sysClose(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	e := h.fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindStdin || e.kind == fdKindStdout || e.kind == fdKindStderr {
		req.Reply(EOK)
		return
	}
	if e.handle != 0 {
		h.fs.Close(e.handle)
	}
	h.fdt.free(fd)
	req.Reply(EOK)
}

func (h *syscallHandler) sysLseek(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	offset := int64(req.Args[1])
	whence := int(req.Args[2])

	e := h.fdt.get(fd)
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
	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	cwd := h.fdt.cwd
	n := copy(buf, cwd)
	if n < len(buf) {
		buf[n] = 0
		n++
	}
	req.Reply(int64(n))
}

func (h *syscallHandler) sysChdir(req sys.SyscallRequest) {
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)

	isDir, _, err := h.fs.Resolve(absPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if !isDir {
		req.Reply(ENOTDIR)
		return
	}

	h.fdt.cwd = absPath
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchdir(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysIoctl(req sys.SyscallRequest) {
	req.Reply(ENOTTY)
}

func (h *syscallHandler) sysFsync(req sys.SyscallRequest) {
	if err := h.fs.Sync(); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

// ============================================================
// Metadata syscalls (via fs IPC)
// ============================================================

func (h *syscallHandler) sysOpenat(req sys.SyscallRequest) {
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	flags := int32(req.Args[2])
	mode := uint32(req.Args[3])
	absPath := h.fdt.resolvePath(path)
	if absPath == "" {
		req.Reply(EINVAL)
		return
	}

	handle, ftype, size, err := h.fs.Open(absPath, uint32(flags), mode)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	newFD := h.fdt.alloc(3)
	if newFD < 0 {
		h.fs.Close(handle)
		req.Reply(EMFILE)
		return
	}

	kind := fdKindFile
	if ftype == ftDir {
		kind = fdKindDir
	}

	h.fdt.put(newFD, &fdEntry{
		kind:   kind,
		handle: handle,
		size:   size,
		ftype:  ftype,
		flags:  flags,
	})
	req.Reply(int64(newFD))
}

func (h *syscallHandler) sysFstat(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	e := h.fdt.get(fd)
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}

	absPath := h.fdt.resolvePath(path)
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)
	if err := h.fs.Mkdir(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUnlinkat(req sys.SyscallRequest) {
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)
	if err := h.fs.Remove(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysRenameat(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysFtruncate(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysGetdents64(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	e := h.fdt.get(fd)
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)
	if err := h.fs.Access(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchmodat(req sys.SyscallRequest) {
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)
	if err := h.fs.SetMode(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUtimensat(req sys.SyscallRequest) {
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := h.fdt.resolvePath(path)
	if err := h.fs.SetTimes(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysReadlinkat(req sys.SyscallRequest) {
	req.Reply(EINVAL) // no symlinks
}

func (h *syscallHandler) sysStatfs(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysFstatfs(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

// ============================================================
// Data syscalls (via fs IPC)
// ============================================================

func (h *syscallHandler) sysRead(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	e := h.fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind == fdKindStdin {
		// Queue the request for the stdin drainer (main goroutine).
		// Don't reply — the drainer will reply when input arrives.
		reqQueue.Enqueue(&readDataResponse{req: req})
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

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
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

func (h *syscallHandler) sysWrite(req sys.SyscallRequest) {
	fd := int(req.Arg0())
	data := req.Data()

	e := h.fdt.get(fd)
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

	// Copy data to shared area and send write request.
	n := h.fs.WriteData(data)

	written, err := h.fs.Write(e.handle, e.offset, n)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	e.offset += int64(written)
	// Update cached size if file grew.
	if uint32(e.offset) > e.size {
		e.size = uint32(e.offset)
	}
	req.Reply(int64(written))
}

func (h *syscallHandler) sysWritev(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysReadv(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

// ============================================================
// Helpers
// ============================================================

// fillStdioStatBuf writes a minimal stat struct for stdin/stdout/stderr.
func fillStdioStatBuf(buf []byte) {
	for i := range buf[:128] {
		buf[i] = 0
	}
	// st_mode = S_IFCHR | 0666
	le := binary.LittleEndian
	le.PutUint32(buf[16:], 0020666)
}

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
