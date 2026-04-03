package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"mazzy/mazarin/sys"
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
		sys.UartWriteString(fmt.Sprintf("[fs:ipc] request from unconnected SID=%d, dropping\n", sid))
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
		sys.UartWriteString(fmt.Sprintf("[fs:ipc] Send response to SID=%d failed: %v\n", sid, err))
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
				resp.Err = ext2ToErrno(createErr)
				return
			}
			h := &fsHandle{kind: kind, inum: f.InodeNum(), size: f.Size(), ftype: ext2.FTFile}
			f.Close()
			handle := conn.allocHandle(h)
			if handle == 0 {
				resp.Err = -24 // EMFILE
				return
			}
			resp.Result0 = uint64(handle)
			resp.Result1 = uint64(h.ftype)<<32 | uint64(h.size)
			return
		}
		resp.Err = ext2ToErrno(err)
		return
	}
	inode, err := fsys.ReadInode(inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	h := &fsHandle{kind: kind, inum: inum, size: inode.Size, ftype: ext2.FTFile, isDir: inode.IsDir()}
	if inode.IsDir() {
		h.ftype = ext2.FTDir
	}
	handle := conn.allocHandle(h)
	if handle == 0 {
		resp.Err = -24
		return
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
		resp.Err = ext2ToErrno(err)
		return
	}
	h.size = f.Size()
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
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.Mkdir(relPath, uint16(req.Mode)))
}

func (s *fsIPCServer) ipcRemove(conn *fsIPCConn, req *ipc.FSIPCReqPayload, resp *ipc.FSIPCRespPayload, mt *mountTable) {
	path := pathFromReq(conn, req)
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.Remove(relPath))
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
func writeStatBuf(buf []byte, inode *ext2.Inode, inum uint32) {
	for i := range buf[:128] {
		buf[i] = 0
	}
	le := binary.LittleEndian
	le.PutUint64(buf[0:], 0)                        // st_dev
	le.PutUint64(buf[8:], uint64(inum))              // st_ino
	le.PutUint32(buf[16:], uint32(inode.Mode))       // st_mode
	le.PutUint32(buf[20:], uint32(inode.LinksCount)) // st_nlink
	le.PutUint32(buf[24:], uint32(inode.UID))        // st_uid
	le.PutUint32(buf[28:], uint32(inode.GID))        // st_gid
	le.PutUint64(buf[32:], 0)                        // st_rdev
	le.PutUint64(buf[40:], 0)                        // padding
	le.PutUint64(buf[48:], uint64(inode.Size))       // st_size
	le.PutUint32(buf[56:], 4096)                     // st_blksize
	le.PutUint32(buf[60:], 0)                        // padding
	le.PutUint64(buf[64:], uint64(inode.Blocks))     // st_blocks
	le.PutUint64(buf[72:], uint64(inode.ATime))      // st_atime
	le.PutUint64(buf[80:], 0)                        // st_atime_nsec
	le.PutUint64(buf[88:], uint64(inode.MTime))      // st_mtime
	le.PutUint64(buf[96:], 0)                        // st_mtime_nsec
	le.PutUint64(buf[104:], uint64(inode.CTime))     // st_ctime
	le.PutUint64(buf[112:], 0)                       // st_ctime_nsec
}

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
