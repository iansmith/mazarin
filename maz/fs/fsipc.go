package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"mazzy/mazarin/uring"
	"mazzy/shared/fs/ext2"
	"mazzy/shared/ipc"
)

const maxFSHandles = 256

// fsHandle tracks one open file on the fs side.
type fsHandle struct {
	kind  mountKind
	inum  uint32 // ext2 inode number
	size  uint32
	ftype uint8 // ext2.FTFile or ext2.FTDir
	isDir bool
	path  string // original open path — used by tmpTrace to gate diagnostics
}

// tmpTrace prints a single-line diagnostic when the path lives under /tmp.
// Used to capture the fs operation sequence preceding bleve's SCORCH ENOENT
// errors (Bug B in findings.md).
//
// Safe to call from inside an IPC handler: linux's delegate dispatcher peels
// fd≤2 Writes onto a stdout lane that doesn't block on fsclient, so fs can
// log during a handler without deadlocking against linux waiting for our
// response. See findings.md "Fs ↔ Linux Delegate Handler Deadlock".
func tmpTrace(format string, args ...any) {
	fmt.Printf("[fs:tmp] "+format+"\n", args...)
}

func isTmpPath(p string) bool {
	return len(p) >= 4 && p[:4] == "/tmp" && (len(p) == 4 || p[4] == '/')
}

// fsIPCConn tracks one connected shepherd's IPC state.
type fsIPCConn struct {
	sid     int16   // sender shepherd ID
	dataVA  uintptr // shared data area VA in our (fs's) address space
	dataLen int
	handles [maxFSHandles]*fsHandle
	nextHnd uint32
}

func (c *fsIPCConn) dataArea() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(c.dataVA)), c.dataLen)
}

// --- Handle table (per-connection) ---

func (c *fsIPCConn) allocHandle(h *fsHandle) uint32 {
	for i := uint32(0); i < maxFSHandles; i++ {
		idx := (c.nextHnd + i) % maxFSHandles
		if c.handles[idx] == nil {
			c.handles[idx] = h
			c.nextHnd = (idx + 1) % maxFSHandles
			return idx + 1 // 1-based
		}
	}
	return 0
}

func (c *fsIPCConn) getHandle(handle uint32) *fsHandle {
	if handle == 0 || handle > maxFSHandles {
		return nil
	}
	return c.handles[handle-1]
}

func (c *fsIPCConn) freeHandle(handle uint32) {
	if handle > 0 && handle <= maxFSHandles {
		c.handles[handle-1] = nil
	}
}

// --- Uring IPC server ---

// fsIPCServer manages per-shepherd connections and processes file operation
// requests delivered via uring (ProtoFSIPCReq). Each connected shepherd has
// its own handle table and shared data area.
type fsIPCServer struct {
	conns map[int16]*fsIPCConn
}

func newFsIPCServer() *fsIPCServer {
	return &fsIPCServer{
		conns: make(map[int16]*fsIPCConn),
	}
}

// DecodeReq is the uring Dispatcher decoder for ProtoFSIPCReq messages.
// Returns a fsIPCRequest wrapping the payload and sender info.
func DecodeReq(msg *ipc.UringIPCMsg) any {
	return fsIPCRequest{
		senderSID: msg.SenderSID,
		payload:   *ipc.DecodeFSIPCReq(msg),
	}
}

// fsIPCRequest pairs a decoded payload with sender identification.
type fsIPCRequest struct {
	senderSID int16
	payload   ipc.FSIPCReqPayload
}

// processRequest handles one uring-delivered file operation request.
func (s *fsIPCServer) processRequest(raw fsIPCRequest, mt *mountTable) {
	req := &raw.payload
	sid := raw.senderSID

	// Handle OpConnect — establish a new connection.
	if req.Op == ipc.FSOpConnect {
		s.handleConnect(sid, req)
		return
	}

	conn := s.conns[sid]
	if conn == nil {
		fmt.Printf("[fs:ipc] request from unconnected SID=%d, dropping\n", sid)
		return
	}

	var resp ipc.FSIPCRespPayload
	resp.ReqID = req.ReqID

	switch req.Op {
	case ipc.FSOpOpen:
		s.ipcOpen(conn, req, &resp, mt)
	case ipc.FSOpClose:
		s.ipcClose(conn, req, &resp)
	case ipc.FSOpRead:
		s.ipcRead(conn, req, &resp, mt)
	case ipc.FSOpWrite:
		s.ipcWrite(conn, req, &resp, mt)
	case ipc.FSOpStat:
		s.ipcStat(conn, req, &resp, mt)
	case ipc.FSOpFstat:
		s.ipcFstat(conn, req, &resp, mt)
	case ipc.FSOpMkdir:
		s.ipcMkdir(conn, req, &resp, mt)
	case ipc.FSOpRemove:
		s.ipcRemove(conn, req, &resp, mt)
	case ipc.FSOpRename:
		s.ipcRename(conn, req, &resp, mt)
	case ipc.FSOpReadDir:
		s.ipcReadDir(conn, req, &resp, mt)
	case ipc.FSOpAccess:
		s.ipcAccess(conn, req, &resp, mt)
	case ipc.FSOpResolve:
		s.ipcResolve(conn, req, &resp, mt)
	case ipc.FSOpSetMode:
		s.ipcSetMode(conn, req, &resp, mt)
	case ipc.FSOpSetTimes:
		s.ipcSetTimes(conn, req, &resp, mt)
	case ipc.FSOpSync:
		s.ipcSync(conn, req, &resp, mt)
	case ipc.FSOpTruncate:
		s.ipcTruncate(conn, req, &resp, mt)
	default:
		resp.Err = -38 // ENOSYS
	}

	s.respond(sid, &resp)
}

// handleConnect registers a new shepherd connection.
func (s *fsIPCServer) handleConnect(sid int16, req *ipc.FSIPCReqPayload) {
	s.conns[sid] = &fsIPCConn{
		sid:     sid,
		dataVA:  uintptr(req.DataVA),
		dataLen: int(req.DataLen),
	}
	fmt.Printf("[fs] IPC connect from SID=%d dataVA=0x%x len=%d\n", sid, req.DataVA, req.DataLen)

	resp := ipc.FSIPCRespPayload{ReqID: req.ReqID}
	s.respond(sid, &resp)
}

// respond sends a ProtoFSIPCResp back to the requesting shepherd via uring.
func (s *fsIPCServer) respond(sid int16, resp *ipc.FSIPCRespPayload) {
	msg := ipc.EncodeFSIPCResp(resp)
	if err := uring.Send(int(sid), &msg); err != nil {
		fmt.Printf("[fs:ipc] Send response to SID=%d failed: %v\n", sid, err)
	}
}

// pathFromReq reads the null-terminated path from the connection's data area.
func pathFromReq(conn *fsIPCConn, req *ipc.FSIPCReqPayload) string {
	if req.PathLen == 0 {
		return ""
	}
	area := conn.dataArea()
	n := int(req.PathLen)
	if n > len(area) {
		n = len(area)
	}
	// Strip null terminator.
	for i := 0; i < n; i++ {
		if area[i] == 0 {
			return string(area[:i])
		}
	}
	return string(area[:n])
}

// --- Operation handlers ---

func (s *fsIPCServer) ipcOpen(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	flags := req.Flags
	mode := uint16(req.Mode)

	const oCREAT = 0x40

	inum, err := fsys.ResolveInode(relPath)
	if err != nil {
		if err == ext2.ErrNotFound && (flags&oCREAT) != 0 {
			f, createErr := fsys.Create(relPath, mode|ext2.PermOwnerRW)
			if createErr != nil {
				if isTmpPath(path) {
					tmpTrace("OPEN+CREAT FAIL path=%s rel=%s create=%v", path, relPath, createErr)
				}
				fmt.Printf("[fs:open] O_CREAT failed: path=%s rel=%s resolve=%v create=%v\n",
					path, relPath, err, createErr)
				resp.Err = ext2ToErrno(createErr)
				return
			}
			h := &fsHandle{kind: kind, inum: f.InodeNum(), size: f.Size(), ftype: ext2.FTFile, path: path}
			f.Close()
			handle := conn.allocHandle(h)
			if handle == 0 {
				resp.Err = -24 // EMFILE
				return
			}
			if isTmpPath(path) {
				tmpTrace("OPEN+CREAT ok path=%s inum=%d handle=%d", path, h.inum, handle)
			}
			resp.Result0 = uint64(handle)
			resp.Result1 = uint64(h.ftype)<<32 | uint64(h.size)
			return
		}
		if isTmpPath(path) {
			tmpTrace("OPEN FAIL path=%s rel=%s flags=0x%x err=%v", path, relPath, flags, err)
		}
		resp.Err = ext2ToErrno(err)
		return
	}
	inode, err := fsys.ReadInode(inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	h := &fsHandle{kind: kind, inum: inum, size: inode.Size, ftype: ext2.FTFile, isDir: inode.IsDir(), path: path}
	if inode.IsDir() {
		h.ftype = ext2.FTDir
	}
	handle := conn.allocHandle(h)
	if handle == 0 {
		resp.Err = -24
		return
	}
	if isTmpPath(path) {
		tmpTrace("OPEN ok path=%s inum=%d size=%d handle=%d isDir=%v", path, h.inum, h.size, handle, h.isDir)
	}
	resp.Result0 = uint64(handle)
	resp.Result1 = uint64(h.ftype)<<32 | uint64(h.size)
}

func (s *fsIPCServer) ipcClose(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload) {
	conn.freeHandle(req.Handle)
}

func (s *fsIPCServer) ipcRead(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	h := conn.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9 // EBADF
		return
	}
	area := conn.dataArea()
	offset := req.Arg0
	count := int(req.Arg1)
	if count > len(area) {
		count = len(area)
	}

	fsys := mt.getFS(h.kind)
	f, err := fsys.OpenInum(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	defer f.Close()
	_ = f.Seek(uint64(offset))
	n, err := f.Read(area[:count])
	if err != nil && err != ext2.ErrEndOfFile {
		resp.Err = ext2ToErrno(err)
		return
	}
	resp.DataLen = uint32(n)
}

func (s *fsIPCServer) ipcWrite(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	h := conn.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9
		return
	}
	area := conn.dataArea()
	offset := req.Arg0
	count := int(req.DataLen)
	if count > len(area) {
		count = len(area)
	}

	fsys := mt.getFS(h.kind)
	f, err := fsys.OpenInum(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	defer f.Close()
	_ = f.Seek(uint64(offset))
	n, err := f.Write(area[:count])
	if err != nil {
		if isTmpPath(h.path) {
			tmpTrace("WRITE FAIL handle=%d path=%s off=%d count=%d err=%v",
				req.Handle, h.path, offset, count, err)
		}
		resp.Err = ext2ToErrno(err)
		return
	}
	h.size = f.Size()
	// Note: successful WRITE is intentionally NOT traced — bleve does
	// tens of thousands of writes per import and tmpTrace goes to UART
	// (slow polled output). Tracing every write blocks fs.maz on UART
	// drain and freezes the whole system. WRITE FAIL is still traced
	// because failures are rare and diagnostically valuable.
	resp.Result0 = uint64(n)
}

func (s *fsIPCServer) ipcStat(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)

	inum, err := fsys.ResolveInode(relPath)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	inode, err := fsys.ReadInode(inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	writeStatBuf(conn.dataArea(), inode, inum)
	resp.DataLen = 128
}

func (s *fsIPCServer) ipcFstat(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	h := conn.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9
		return
	}

	fsys := mt.getFS(h.kind)
	inode, err := fsys.ReadInode(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	writeStatBuf(conn.dataArea(), inode, h.inum)
	resp.DataLen = 128
}

func (s *fsIPCServer) ipcMkdir(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	// An empty relPath means path is exactly a mount-point root — it already exists.
	if relPath == "" || relPath == "/" {
		resp.Err = -17 // EEXIST
		return
	}
	fsys := mt.getFS(kind)
	err := fsys.Mkdir(relPath, uint16(req.Mode))
	resp.Err = ext2ToErrno(err)
}

func (s *fsIPCServer) ipcRemove(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	err := fsys.Remove(relPath)
	if isTmpPath(path) {
		if err != nil {
			tmpTrace("REMOVE FAIL path=%s err=%v", path, err)
		} else {
			tmpTrace("REMOVE ok path=%s", path)
		}
	}
	resp.Err = ext2ToErrno(err)
}

func (s *fsIPCServer) ipcRename(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	// Data area contains "oldpath\0newpath\0". Arg0 = offset of newpath.
	area := conn.dataArea()
	newOff := int(req.Arg0)
	if newOff <= 0 || newOff >= len(area) {
		resp.Err = -22 // EINVAL
		return
	}
	// Extract old path.
	var oldPath string
	for i := 0; i < newOff; i++ {
		if area[i] == 0 {
			oldPath = string(area[:i])
			break
		}
	}
	// Extract new path.
	var newPath string
	for i := newOff; i < len(area); i++ {
		if area[i] == 0 {
			newPath = string(area[newOff:i])
			break
		}
	}
	if oldPath == "" || newPath == "" {
		resp.Err = -22 // EINVAL
		return
	}
	// Both paths must resolve to the same mount.
	oldKind, oldRel := mt.resolve(oldPath)
	newKind, newRel := mt.resolve(newPath)
	if oldKind != newKind {
		resp.Err = -18 // EXDEV — cross-device rename
		return
	}
	fsys := mt.getFS(oldKind)
	err := fsys.Rename(oldRel, newRel)
	if isTmpPath(oldPath) || isTmpPath(newPath) {
		if err != nil {
			tmpTrace("RENAME FAIL old=%s new=%s err=%v", oldPath, newPath, err)
		} else {
			tmpTrace("RENAME ok old=%s new=%s", oldPath, newPath)
		}
	}
	resp.Err = ext2ToErrno(err)
}

func (s *fsIPCServer) ipcReadDir(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	h := conn.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9
		return
	}
	if !h.isDir {
		resp.Err = -20 // ENOTDIR
		return
	}

	fsys := mt.getFS(h.kind)
	entries, err := fsys.ReadDir(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	startIdx := int(req.Arg0)
	n, count := marshalDirents(conn.dataArea(), entries, startIdx)
	resp.DataLen = uint32(n)
	resp.Result0 = uint64(count)
}

func (s *fsIPCServer) ipcAccess(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	_, err := fsys.ResolveInode(relPath)
	resp.Err = ext2ToErrno(err)
}

func (s *fsIPCServer) ipcResolve(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)

	inum, err := fsys.ResolveInode(relPath)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	inode, err := fsys.ReadInode(inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	isDir := uint64(0)
	if inode.IsDir() {
		isDir = 1
	}
	resp.Result0 = isDir
	resp.Result1 = uint64(inode.Size)
}

func (s *fsIPCServer) ipcSetMode(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.SetMode(relPath, uint16(req.Mode)))
}

func (s *fsIPCServer) ipcSetTimes(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.SetTimes(relPath, 0, 0))
}

func (s *fsIPCServer) ipcSync(_ *fsIPCConn, _ *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	if mt.tmpFS != nil {
		resp.Err = ext2ToErrno(mt.tmpFS.Sync())
	}
}

func (s *fsIPCServer) ipcTruncate(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	h := conn.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9 // EBADF
		return
	}
	fsys := mt.getFS(h.kind)
	newSize := uint32(req.Arg0)
	if err := fsys.Truncate(h.inum, newSize); err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	h.size = newSize
}

// --- Helpers ---

// ext2ToErrno converts ext2 errors to negative errno (int32).
func ext2ToErrno(err error) int32 {
	if err == nil {
		return 0
	}
	switch err {
	case ext2.ErrNotFound:
		return -2
	case ext2.ErrExists:
		return -17
	case ext2.ErrNotDir:
		return -20
	case ext2.ErrNotFile:
		return -21
	case ext2.ErrNoSpace:
		return -28
	case ext2.ErrNoInodes:
		return -28
	case ext2.ErrNotEmpty:
		return -39
	case ext2.ErrNameTooLong:
		return -36
	case ext2.ErrReadOnly:
		return -30
	case ext2.ErrInvalidPath:
		return -22
	case ext2.ErrInvalidInode:
		return -22
	case ext2.ErrEndOfFile:
		return 0
	default:
		return -5 // EIO
	}
}

// writeStatBuf writes a 128-byte Linux struct stat from an ext2 inode.
// Arch-specific: ARM64/RISC-V and x86_64 have different struct stat layouts.
// See stat_arm64.go / stat_amd64.go / stat_riscv64.go.

// marshalDirents writes ext2 directory entries as linux_dirent64 into buf.
// Starts from startIdx, returns (bytes written, entries written).
func marshalDirents(buf []byte, entries []ext2.DirEntry, startIdx int) (int, int) {
	le := binary.LittleEndian
	off := 0
	count := 0
	for i := startIdx; i < len(entries); i++ {
		de := &entries[i]
		nameLen := len(de.Name)
		reclen := 8 + 8 + 2 + 1 + nameLen + 1 // ino + off + reclen + type + name + null
		reclen = (reclen + 7) &^ 7             // align to 8
		if off+reclen > len(buf) {
			break
		}
		le.PutUint64(buf[off:], uint64(de.Inode))
		le.PutUint64(buf[off+8:], uint64(i+1))
		le.PutUint16(buf[off+16:], uint16(reclen))
		buf[off+18] = de.FileType
		copy(buf[off+19:], de.Name)
		buf[off+19+nameLen] = 0
		for j := off + 19 + nameLen + 1; j < off+reclen; j++ {
			buf[j] = 0
		}
		off += reclen
		count++
	}
	return off, count
}
