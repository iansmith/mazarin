package main

import (
	"encoding/binary"
	"unsafe"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/sys"
	"mazzy/shared/dlist"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
)

// syscallHandler processes delegated file syscalls for the linux shepherd.
// Per-shepherd filesystem state (FD tables, flocks, CWD) is tracked in
// ShepherdFilesystemData, keyed by caller SID.
type syscallHandler struct {
	shepherds map[int16]*ShepherdFilesystemData
	flocks    *flockTable
	fs        *fsclient.Client
}

func newSyscallHandler(fs *fsclient.Client) *syscallHandler {
	return &syscallHandler{
		shepherds: make(map[int16]*ShepherdFilesystemData),
		flocks:    newFlockTable(),
		fs:        fs,
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
	case sysid.MmapPageWriteback:
		h.sysMmapPageWriteback(req)

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
	if e.handle != 0 {
		h.fs.Close(e.handle)
	}
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	flags := int32(req.Args[2])
	mode := uint32(req.Args[3])
	absPath := fdt.resolvePath(path)
	if absPath == "" {
		req.Reply(EINVAL)
		return
	}

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

	fdt.put(newFD, &fdEntry{
		kind:   kind,
		handle: handle,
		size:   size,
		ftype:  ftype,
		flags:  flags,
		path:   absPath,
	})
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}

	absPath := fdt.resolvePath(path)
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
	if err := h.fs.Mkdir(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUnlinkat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
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
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
	if err := h.fs.Access(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchmodat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
	if err := h.fs.SetMode(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUtimensat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.resolvePath(path)
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
	n := h.fs.WriteData(data)

	written, err := h.fs.Write(e.handle, offset, n)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Update cached size if file grew.
	endPos := offset + int64(written)
	if uint32(endPos) > e.size {
		e.size = uint32(endPos)
	}
	req.Reply(int64(written))
}

// sysMmapPageFill handles kernel requests to fill a file-backed mmap page.
// The kernel allocates a physical frame and maps it into our address space,
// then sends this request so we read file data into it. The kernel's reply
// path unmaps the page from us and maps it read-only into the faulting shepherd.
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

// sysMmapPageWriteback handles batch write-back of MAP_SHARED mmap pages.
// The kernel sends a descriptor page containing an array of MmapWritebackEntry
// structs, each describing a contiguous data region mapped into our address space.
// We iterate the entries and pwrite64 each one back to the file.
//
// Args[0]=fd, Args[1]=descriptorVA, Args[2]=numEntries
// CallerPID = shepherd that owns the fd
func (h *syscallHandler) sysMmapPageWriteback(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Args[0])
	e := fdt.get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.kind != fdKindFile {
		req.Reply(ESPIPE)
		return
	}

	descriptorVA := uintptr(req.Args[1])
	numEntries := int(req.Args[2])
	if descriptorVA == 0 || numEntries == 0 {
		req.Reply(EINVAL)
		return
	}
	if numEntries > ipc.MmapWritebackEntriesPerPage {
		numEntries = ipc.MmapWritebackEntriesPerPage
	}

	// Read the descriptor array from the mapped page
	entries := unsafe.Slice((*ipc.MmapWritebackEntry)(unsafe.Pointer(descriptorVA)), numEntries)

	var totalWritten int64
	for i := 0; i < numEntries; i++ {
		entry := &entries[i]
		if entry.DataVA == 0 || entry.Length == 0 {
			continue
		}
		data := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(entry.DataVA))), entry.Length)
		n := h.fs.WriteData(data)
		written, err := h.fs.Write(e.handle, int64(entry.FileOffset), n)
		if err != nil {
			req.Reply(int64(errToErrno(err)))
			return
		}
		totalWritten += int64(written)
		// Update cached size if file grew
		endPos := int64(entry.FileOffset) + int64(written)
		if uint32(endPos) > e.size {
			e.size = uint32(endPos)
		}
	}
	req.Reply(totalWritten)
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
