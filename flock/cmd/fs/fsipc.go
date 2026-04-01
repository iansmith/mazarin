package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/fs/ext2"
	"mazzy/shared/fs/fsipc"
	"mazzy/shared/hid"
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

// fsIPC manages the fs shepherd's side of the linux↔fs IPC channel.
type fsIPC struct {
	reqRing      *ringbuf.RingBuffer
	respRing     *ringbuf.RingBuffer
	respAddr     uintptr // respRing page VA in fs's space (for MailboxSend)
	dataArea     []byte  // shared data area (4KB)
	linuxSID     int
	requestCh    chan fsipc.Request
	handles      [maxFSHandles]*fsHandle
	nextHnd      uint32
	blockNotifyCh chan struct{} // forwarded to DMA worker on BlockIOCompleteCode
}

func newFsIPC(blockNotifyCh chan struct{}) *fsIPC {
	return &fsIPC{
		requestCh:     make(chan fsipc.Request, 8),
		blockNotifyCh: blockNotifyCh,
	}
}

// mailboxLoop runs in a dedicated goroutine, receiving mailbox notifications
// from the linux shepherd. It handles the handshake and forwards IPC requests
// to the requestCh for processing by the main goroutine.
func (s *fsIPC) mailboxLoop() {
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			continue
		}
		switch notif.Code {
		case hid.BlockIOCompleteCode:
			// Forward block I/O completion to DMA worker (non-blocking send).
			select {
			case s.blockNotifyCh <- struct{}{}:
			default:
			}
		case fsipc.NotifyInit:
			s.handleInit(notif)
		case fsipc.NotifyRequest:
			if s.reqRing == nil {
				sys.UartWriteString("[fs:ipc] NotifyRequest but reqRing is nil!\n")
				continue
			}
			var req fsipc.Request
			for s.reqRing.Pop(unsafe.Pointer(&req)) {
				s.requestCh <- req
			}
		}
	}
}

// handleInit processes the handshake from the linux shepherd.
func (s *fsIPC) handleInit(notif sys.MailboxNotification) {
	s.linuxSID = int(notif.SenderSID)
	s.reqRing = ringbuf.Open(uintptr(notif.RingAddr))
	fmt.Printf("[fs] IPC init from linux SID=%d\n", s.linuxSID)

	// Pop the handshake request.
	var req fsipc.Request
	if !s.reqRing.Pop(unsafe.Pointer(&req)) {
		fmt.Println("[fs] IPC: no handshake message in ring")
		return
	}

	// Create response ring (fs-owned, mapped into linux).
	respPage, err := mem.AllocPages(1, mem.PageIPC)
	if err != nil {
		fmt.Printf("[fs] IPC: alloc response ring failed: %v\n", err)
		return
	}
	*(*byte)(unsafe.Pointer(uintptr(respPage))) = 0
	respPageLinux, err := sys.MailboxMapPage(s.linuxSID, uintptr(respPage))
	if err != nil {
		fmt.Printf("[fs] IPC: map response ring failed: %v\n", err)
		return
	}
	s.respRing = ringbuf.Init(uintptr(respPage), fsipc.SlotSize, fsipc.SlotCount)
	s.respAddr = uintptr(respPage)

	// Create shared data area (fs-owned, mapped into linux).
	dataPage, err := mem.AllocPages(fsipc.DataPages, mem.PageIPC)
	if err != nil {
		fmt.Printf("[fs] IPC: alloc data area failed: %v\n", err)
		return
	}
	*(*byte)(unsafe.Pointer(uintptr(dataPage))) = 0
	dataPageLinux, err := sys.MailboxMapPage(s.linuxSID, uintptr(dataPage))
	if err != nil {
		fmt.Printf("[fs] IPC: map data area failed: %v\n", err)
		return
	}
	s.dataArea = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(dataPage))), fsipc.DataPages*4096)

	// Push handshake ack with translated addresses for linux.
	var ack fsipc.Response
	ack.ID = 0
	ack.Result0 = uint64(respPageLinux)
	ack.Result1 = uint64(dataPageLinux)
	ack.DataLen = uint32(fsipc.DataPages * 4096)
	s.respRing.Push(unsafe.Pointer(&ack))

	if err := sys.MailboxSend(s.linuxSID, fsipc.NotifyReady, s.respAddr); err != nil {
		fmt.Printf("[fs] IPC: MailboxSend NotifyReady failed: %v\n", err)
		return
	}
	fmt.Printf("[fs] IPC ready: resp=0x%x(linux:0x%x) data=0x%x(linux:0x%x)\n",
		respPage, respPageLinux, dataPage, dataPageLinux)
}

// respond pushes a response and notifies linux.
func (s *fsIPC) respond(resp *fsipc.Response) {
	s.respRing.Push(unsafe.Pointer(resp))
	err := sys.MailboxSend(s.linuxSID, fsipc.NotifyResponse, s.respAddr)
	if err != nil {
		sys.UartWriteString("[fs:ipc] MailboxSend response FAILED\n")
	}
}

// --- Handle table ---

func (s *fsIPC) allocHandle(h *fsHandle) uint32 {
	for i := uint32(0); i < maxFSHandles; i++ {
		idx := (s.nextHnd + i) % maxFSHandles
		if s.handles[idx] == nil {
			s.handles[idx] = h
			s.nextHnd = (idx + 1) % maxFSHandles
			return idx + 1 // 1-based
		}
	}
	return 0
}

func (s *fsIPC) getHandle(handle uint32) *fsHandle {
	if handle == 0 || handle > maxFSHandles {
		return nil
	}
	return s.handles[handle-1]
}

func (s *fsIPC) freeHandle(handle uint32) {
	if handle > 0 && handle <= maxFSHandles {
		s.handles[handle-1] = nil
	}
}

// --- Request dispatch ---

func (s *fsIPC) processRequest(req *fsipc.Request, mt *mountTable) {
	var resp fsipc.Response
	resp.ID = req.ID

	switch req.Op {
	case fsipc.OpOpen:
		s.ipcOpen(req, &resp, mt)
	case fsipc.OpClose:
		s.ipcClose(req, &resp)
	case fsipc.OpRead:
		s.ipcRead(req, &resp, mt)
	case fsipc.OpWrite:
		s.ipcWrite(req, &resp, mt)
	case fsipc.OpStat:
		s.ipcStat(req, &resp, mt)
	case fsipc.OpFstat:
		s.ipcFstat(req, &resp, mt)
	case fsipc.OpMkdir:
		s.ipcMkdir(req, &resp, mt)
	case fsipc.OpRemove:
		s.ipcRemove(req, &resp, mt)
	case fsipc.OpReadDir:
		s.ipcReadDir(req, &resp, mt)
	case fsipc.OpAccess:
		s.ipcAccess(req, &resp, mt)
	case fsipc.OpResolve:
		s.ipcResolve(req, &resp, mt)
	case fsipc.OpSetMode:
		s.ipcSetMode(req, &resp, mt)
	case fsipc.OpSetTimes:
		s.ipcSetTimes(req, &resp, mt)
	case fsipc.OpSync:
		s.ipcSync(req, &resp, mt)
	default:
		resp.Err = -38 // ENOSYS
	}

	s.respond(&resp)
}

// --- Operation handlers ---

func (s *fsIPC) ipcOpen(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
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
			handle := s.allocHandle(h)
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
	handle := s.allocHandle(h)
	if handle == 0 {
		resp.Err = -24
		return
	}
	resp.Result0 = uint64(handle)
	resp.Result1 = uint64(h.ftype)<<32 | uint64(h.size)
}

func (s *fsIPC) ipcClose(req *fsipc.Request, resp *fsipc.Response) {
	s.freeHandle(req.Handle)
}

func (s *fsIPC) ipcRead(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	h := s.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9 // EBADF
		return
	}
	offset := req.Arg0
	count := int(req.Arg1)
	if count > len(s.dataArea) {
		count = len(s.dataArea)
	}

	fsys := mt.getFS(h.kind)
	f, err := fsys.OpenInum(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	defer f.Close()
	_ = f.Seek(uint64(offset))
	n, err := f.Read(s.dataArea[:count])
	if err != nil && err != ext2.ErrEndOfFile {
		resp.Err = ext2ToErrno(err)
		return
	}
	resp.DataLen = uint32(n)
}

func (s *fsIPC) ipcWrite(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	h := s.getHandle(req.Handle)
	if h == nil {
		resp.Err = -9
		return
	}
	offset := req.Arg0
	count := int(req.DataLen)
	if count > len(s.dataArea) {
		count = len(s.dataArea)
	}

	fsys := mt.getFS(h.kind)
	f, err := fsys.OpenInum(h.inum)
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	defer f.Close()
	_ = f.Seek(uint64(offset))
	n, err := f.Write(s.dataArea[:count])
	if err != nil {
		resp.Err = ext2ToErrno(err)
		return
	}
	h.size = f.Size()
	resp.Result0 = uint64(n)
}

func (s *fsIPC) ipcStat(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
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
	writeStatBuf(s.dataArea, inode, inum)
	resp.DataLen = 128
}

func (s *fsIPC) ipcFstat(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	h := s.getHandle(req.Handle)
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
	writeStatBuf(s.dataArea, inode, h.inum)
	resp.DataLen = 128
}

func (s *fsIPC) ipcMkdir(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.Mkdir(relPath, uint16(req.Mode)))
}

func (s *fsIPC) ipcRemove(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.Remove(relPath))
}

func (s *fsIPC) ipcReadDir(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	h := s.getHandle(req.Handle)
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
	n, count := marshalDirents(s.dataArea, entries, startIdx)
	resp.DataLen = uint32(n)
	resp.Result0 = uint64(count)
}

func (s *fsIPC) ipcAccess(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	_, err := fsys.ResolveInode(relPath)
	resp.Err = ext2ToErrno(err)
}

func (s *fsIPC) ipcResolve(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
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

func (s *fsIPC) ipcSetMode(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.SetMode(relPath, uint16(req.Mode)))
}

func (s *fsIPC) ipcSetTimes(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
	path := fsipc.PathString(req.Path[:])
	kind, relPath := mt.resolve(path)
	fsys := mt.getFS(kind)
	resp.Err = ext2ToErrno(fsys.SetTimes(relPath, 0, 0))
}

func (s *fsIPC) ipcSync(req *fsipc.Request, resp *fsipc.Response, mt *mountTable) {
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
