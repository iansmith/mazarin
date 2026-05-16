// Package fsclient provides a uring-based IPC client for talking to the fs
// shepherd. Any shepherd that needs file operations creates an FSClient
// and registers its response channel on their uring Dispatcher:
//
//	fc := fsclient.New(fsSID)
//	dispatcher.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.GetRespCh())
//	dispatcher.Start()
//	fc.SetRespRing(ring)
//	fc.Connect()
//
// Bulk data (paths, read/write buffers, stat results) flows through a shared
// data area — caller-owned pages mapped into fs's address space via SharePages.
//
// All public methods are safe to call from multiple goroutines concurrently.
// Each method holds c.mu for the entire setPath / data-area-write / send /
// receive / data-area-read sequence so that concurrent callers can't corrupt
// each other's path or response data. Without this lock the shared data area
// is racy: goroutine A's setPath can be overwritten by goroutine B's setPath
// before A's IPC request reaches fs, producing scrambled paths and the
// EISDIR/ENOENT failures that mimic dirent/inode bookkeeping bugs.
package fsclient

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const dataPages = 16 // shared data area size (64KB)

// FSClient is the interface for IPC with the fs shepherd. Using an interface
// (not a concrete pointer) lets the host's implementation cross .maz module
// boundaries via itabs — method calls on the plugin side resolve through the
// host's type descriptors, so callLocked's ipc.FSIPCRespPayload assertion
// always sees the host's type.
type FSClient interface {
	Connect() error
	SetRespRing(ring uint8)
	GetRespCh() chan any
	DataLen() int

	Open(path string, flags, mode uint32) (handle uint32, inum uint32, ftype uint8, size uint32, err error)
	Close(handle uint32) error
	Read(handle uint32, offset int64, buf []byte) (int, error)
	Write(handle uint32, offset int64, data []byte) (int, error)
	Stat(path string, statBuf []byte) (int32, error)
	Fstat(handle uint32, statBuf []byte) (int32, error)
	Mkdir(path string, mode uint32) error
	Remove(path string) error
	Rename(oldPath, newPath string) error
	ReadDir(handle uint32, startIdx int, buf []byte) (int, int, error)
	Access(path string) error
	SetMode(path string, mode uint32) error
	SetTimes(path string) error
	Truncate(handle uint32, size int64) error
	Sync() error
	Resolve(path string) (isDir bool, size uint32, err error)
	ReadFile(path string) ([]byte, error)
}

// clientImpl is the concrete implementation of FSClient.
type clientImpl struct {
	fsSID int

	// respCh receives decoded FSIPCRespPayload values from the uring Dispatcher.
	respCh chan any

	// respRing is the uring ring index for fs to send responses on.
	respRing uint8

	respChOverride chan any
	reqIDOffset    uint32

	mu sync.Mutex

	localVA  uintptr
	remoteVA uintptr
	dataLen  int
	nextID   uint32
}

// New creates a new fs client targeting the given shepherd ID.
// Call Connect() after the uring Dispatcher is started to establish the link.
//
// RespCh is cap 1: the dispatcher posts at most one response at a time
// because every public method holds c.mu for the full request/response
// cycle, so by construction there's never more than one in-flight call.
// A larger buffer just hides bugs (e.g., a stale reply from a previously
// timed-out call would silently accumulate). Cap 1 makes such anomalies
// surface immediately at the dispatcher's send-side.
func New(fsSID int) FSClient {
	return &clientImpl{
		fsSID:  fsSID,
		respCh: make(chan any, 1),
	}
}

// DecodeResp is the uring Dispatcher decoder for ProtoFSIPCResp messages.
func DecodeResp(msg *ipc.UringIPCMsg) any {
	p := ipc.DecodeFSIPCResp(msg)
	return *p
}

// Connect allocates the shared data area, maps it into fs, and sends the
// handshake message. Blocks until fs confirms the connection.
func (c *clientImpl) Connect() error {
	// Allocate shared data pages.
	ptr, err := mem.AllocPages(dataPages, mem.PageIPC)
	if err != nil {
		return fmt.Errorf("fsclient: AllocPages: %w", err)
	}
	c.localVA = uintptr(ptr)
	c.dataLen = dataPages * 4096

	// Map into fs's address space.
	remote, mapErr := sys.SharePagesWithTarget(c.fsSID, c.localVA, dataPages)
	if mapErr != nil {
		return fmt.Errorf("fsclient: SharePagesWithTarget: %w", mapErr)
	}
	c.remoteVA = remote

	// Send connect handshake.
	c.mu.Lock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:       ipc.FSOpConnect,
		DataVA:   uint64(c.remoteVA),
		DataLen:  uint32(c.dataLen),
		RespRing: c.respRing,
	})
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return fmt.Errorf("fsclient: connect rejected: %d", resp.Err)
	}
	return nil
}

// callLocked sends a request and blocks for the response. Caller must hold
// c.mu — that's how we keep nextID consistent and stop two goroutines from
// each consuming the other's RespCh value.
func (c *clientImpl) callLocked(req *ipc.FSIPCReqPayload) (ipc.FSIPCRespPayload, error) {
	c.nextID++
	req.ReqID = c.nextID + c.reqIDOffset
	if req.DataVA == 0 {
		req.DataVA = uint64(c.remoteVA)
	}

	msg := ipc.EncodeFSIPCReq(req, int16(os.Getpid()))
	if err := uring.Send(c.fsSID, &msg); err != nil {
		return ipc.FSIPCRespPayload{}, fmt.Errorf("fsclient: Send: %w", err)
	}

	respCh := c.respCh
	if c.respChOverride != nil {
		respCh = c.respChOverride
		c.respChOverride = nil
	}
	raw := <-respCh
	resp, ok := raw.(ipc.FSIPCRespPayload)
	if !ok {
		return ipc.FSIPCRespPayload{}, errors.New("fsclient: unexpected response type")
	}
	return resp, nil
}

// DataLen returns the shared data area size in bytes. Constant — safe to
// call without the lock.
func (c *clientImpl) DataLen() int { return c.dataLen }

// SetRespRing sets the uring ring index for fs to send responses on.
func (c *clientImpl) SetRespRing(ring uint8) { c.respRing = ring }

// GetRespCh returns the response channel for use with uring Dispatcher.
func (c *clientImpl) GetRespCh() chan any { return c.respCh }

// dataArea returns the local shared data area as a byte slice. Caller must
// hold c.mu while reading or writing — the returned slice is the live
// shared region and concurrent callers will trample it.
func (c *clientImpl) dataArea() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(c.localVA)), c.dataLen)
}

// setPathLocked writes a null-terminated path to the start of the data area
// and returns the length including the null. Caller must hold c.mu.
func (c *clientImpl) setPathLocked(path string) uint16 {
	area := c.dataArea()
	n := len(path)
	if n >= len(area) {
		n = len(area) - 1
	}
	copy(area[:n], path)
	area[n] = 0
	return uint16(n + 1)
}

// --- File Operations ---

// Open opens a file on the fs shepherd.
// Returns (handle, inum, ftype, size, err). ftype: 1=file, 2=dir.
// inum is the ext2 inode number, exposed so callers can key per-file
// state by inode (matching Linux's VMA-survives-close semantic) rather
// than by the fd-side handle.
func (c *clientImpl) Open(path string, flags, mode uint32) (handle uint32, inum uint32, ftype uint8, size uint32, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, callErr := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpOpen,
		PathLen: pathLen,
		Flags:   flags,
		Mode:    mode,
	})
	if callErr != nil {
		return 0, 0, 0, 0, callErr
	}
	if resp.Err != 0 {
		return 0, 0, 0, 0, ErrnoErr(resp.Err)
	}
	handle = uint32(resp.Result0)
	inum = uint32(resp.Result0 >> 32)
	ftype = uint8(resp.Result1 >> 32)
	size = uint32(resp.Result1 & 0xFFFFFFFF)
	return handle, inum, ftype, size, nil
}

// Close closes a handle.
func (c *clientImpl) Close(handle uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpClose,
		Handle: handle,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Read reads up to len(buf) bytes from handle at offset and copies them
// into buf. Returns the number of bytes read.
//
// The shared data area copy happens under c.mu so a concurrent caller's
// Stat/Read/ReadDir can't overwrite our response data before we've copied
// it into the caller's buf.
func (c *clientImpl) Read(handle uint32, offset int64, buf []byte) (int, error) {
	count := len(buf)
	if count > c.dataLen {
		count = c.dataLen
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpRead,
		Handle: handle,
		Arg0:   uint64(offset),
		Arg1:   uint64(count),
	})
	if err != nil {
		return 0, err
	}
	if resp.Err != 0 {
		return 0, ErrnoErr(resp.Err)
	}
	n := int(resp.DataLen)
	if n > 0 {
		if n > len(buf) {
			n = len(buf)
		}
		copy(buf[:n], c.dataArea()[:n])
	}
	return n, nil
}

// Write writes data to handle at offset. The data area copy and the IPC send
// happen under c.mu so concurrent writers can't interleave.
func (c *clientImpl) Write(handle uint32, offset int64, data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(data)
	if n > c.dataLen {
		n = c.dataLen
	}
	if n > 0 {
		copy(c.dataArea()[:n], data[:n])
	}
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpWrite,
		Handle:  handle,
		Arg0:    uint64(offset),
		DataLen: uint32(n),
	})
	if err != nil {
		return 0, err
	}
	if resp.Err != 0 {
		return 0, ErrnoErr(resp.Err)
	}
	return int(resp.Result0), nil
}

// Stat stats a path. The 128-byte stat buf is copied into statBuf under the
// lock. statBuf must be at least 128 bytes.
func (c *clientImpl) Stat(path string, statBuf []byte) (int32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpStat,
		PathLen: pathLen,
	})
	if err != nil {
		return 0, err
	}
	if resp.Err == 0 && len(statBuf) >= 128 {
		copy(statBuf[:128], c.dataArea()[:128])
	}
	return resp.Err, nil
}

// Fstat stats a handle. The 128-byte stat buf is copied into statBuf under
// the lock. statBuf must be at least 128 bytes.
func (c *clientImpl) Fstat(handle uint32, statBuf []byte) (int32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpFstat,
		Handle: handle,
	})
	if err != nil {
		return 0, err
	}
	if resp.Err == 0 && len(statBuf) >= 128 {
		copy(statBuf[:128], c.dataArea()[:128])
	}
	return resp.Err, nil
}

// Mkdir creates a directory.
func (c *clientImpl) Mkdir(path string, mode uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpMkdir,
		PathLen: pathLen,
		Mode:    mode,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Rename renames oldPath to newPath. Both paths are packed into the data area
// as "oldPath\0newPath\0". Arg0 carries the offset of newPath.
func (c *clientImpl) Rename(oldPath, newPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	area := c.dataArea()
	// Pack: oldpath\0newpath\0
	n := copy(area, oldPath)
	area[n] = 0
	newOff := n + 1
	n2 := copy(area[newOff:], newPath)
	area[newOff+n2] = 0
	totalLen := uint32(newOff + n2 + 1)

	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpRename,
		PathLen: uint16(n + 1), // oldpath + null
		Arg0:    uint64(newOff),
		DataLen: totalLen,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Remove removes a file or directory.
func (c *clientImpl) Remove(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpRemove,
		PathLen: pathLen,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// ReadDir reads directory entries into buf. Returns (bytesCopied,
// entryCount, err). buf is typically a per-caller scratch buffer
// (≥4 KB; fs.maz packs as many dirents as fit in its 64 KB window).
func (c *clientImpl) ReadDir(handle uint32, startIdx int, buf []byte) (int, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpReadDir,
		Handle: handle,
		Arg0:   uint64(startIdx),
	})
	if err != nil {
		return 0, 0, err
	}
	if resp.Err != 0 {
		return 0, 0, ErrnoErr(resp.Err)
	}
	n := int(resp.DataLen)
	if n > len(buf) {
		n = len(buf)
	}
	if n > 0 {
		copy(buf[:n], c.dataArea()[:n])
	}
	return n, int(resp.Result0), nil
}

// Access checks if a path exists.
func (c *clientImpl) Access(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpAccess,
		PathLen: pathLen,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// SetMode changes file permissions.
func (c *clientImpl) SetMode(path string, mode uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpSetMode,
		PathLen: pathLen,
		Mode:    mode,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// SetTimes updates timestamps (no-op currently).
func (c *clientImpl) SetTimes(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpSetTimes,
		PathLen: pathLen,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Truncate sets the size of an open handle.
func (c *clientImpl) Truncate(handle uint32, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpTruncate,
		Handle: handle,
		Arg0:   uint64(size),
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Sync flushes filesystem metadata.
func (c *clientImpl) Sync() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.callLocked(&ipc.FSIPCReqPayload{
		Op: ipc.FSOpSync,
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return ErrnoErr(resp.Err)
	}
	return nil
}

// Resolve checks if a path exists and returns whether it's a directory + size.
func (c *clientImpl) Resolve(path string) (isDir bool, size uint32, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pathLen := c.setPathLocked(path)
	resp, callErr := c.callLocked(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpResolve,
		PathLen: pathLen,
	})
	if callErr != nil {
		return false, 0, callErr
	}
	if resp.Err != 0 {
		return false, 0, ErrnoErr(resp.Err)
	}
	return resp.Result0 != 0, uint32(resp.Result1), nil
}

// ErrnoErr wraps a negative errno as an error. Use IsErrno to extract.
type ErrnoErr int32

func (e ErrnoErr) Error() string { return fmt.Sprintf("errno %d", int32(e)) }

// Errno returns the negative errno value.
func (e ErrnoErr) Errno() int32 { return int32(e) }

// IsErrno extracts the errno from an fsclient error.
// Returns (errno, true) for fs errors, (0, false) otherwise.
//
//go:noinline
func IsErrno(err error) (int32, bool) {
	if e, ok := err.(ErrnoErr); ok {
		return e.Errno(), true
	}
	return 0, false
}

// forceKeepMethods prevents the linker from deadcoding FSClient methods
// that are only called by plugins through the itab. The host binary directly
// calls Connect/SetRespRing/GetRespCh; everything else (Open, Close, Read,
// Write, Stat, Fstat, Mkdir, etc.) is only reachable through the interface.
// Method expressions force the linker to keep them alive.
//
//go:noinline
func forceKeepMethods() {
	_ = (*clientImpl).Open
	_ = (*clientImpl).Close
	_ = (*clientImpl).Read
	_ = (*clientImpl).Write
	_ = (*clientImpl).Stat
	_ = (*clientImpl).Fstat
	_ = (*clientImpl).Mkdir
	_ = (*clientImpl).Remove
	_ = (*clientImpl).Rename
	_ = (*clientImpl).ReadDir
	_ = (*clientImpl).Access
	_ = (*clientImpl).SetMode
	_ = (*clientImpl).SetTimes
	_ = (*clientImpl).Truncate
	_ = (*clientImpl).Sync
	_ = (*clientImpl).Resolve
}

func init() { forceKeepMethods() }

// ReadFile reads an entire file into memory via fsclient (Open + chunked
// Read + Close). It is a convenience wrapper used by callers that previously
// used sys.LoadFile and have been migrated to fsclient IPC.
func (c *clientImpl) ReadFile(path string) ([]byte, error) {
	handle, _, _, size, err := c.Open(path, 0, 0)
	if err != nil {
		return nil, err
	}
	defer c.Close(handle)

	buf := make([]byte, size)
	total := 0
	for offset := int64(0); total < int(size); {
		n, err := c.Read(handle, offset, buf[total:])
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
		total += n
		offset += int64(n)
	}
	return buf[:total], nil
}
