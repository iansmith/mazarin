// Package fsclient provides a uring-based IPC client for talking to the fs
// shepherd. Any shepherd that needs file operations (open, read, write, stat,
// etc.) creates an fsclient.Client and registers its RespCh on their uring
// Dispatcher:
//
//	fc := fsclient.New(fsSID)
//	dispatcher.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.RespCh)
//	dispatcher.Start()
//	fc.Connect()   // handshake with fs
//
// Bulk data (paths, read/write buffers, stat results) flows through a shared
// data area — caller-owned pages mapped into fs's address space via SharePages.
package fsclient

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const dataPages = 1 // shared data area size (4KB)

// Client is an IPC client for the fs shepherd.
type Client struct {
	fsSID int

	// RespCh receives decoded FSIPCRespPayload values from the uring Dispatcher.
	// Register on the Dispatcher before calling Connect:
	//   dispatcher.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.RespCh)
	RespCh chan any

	localVA  uintptr // data area VA in our address space
	remoteVA uintptr // data area VA in fs's address space
	dataLen  int     // data area size in bytes
	nextID   uint32
}

// New creates a new fs client targeting the given shepherd ID.
// Call Connect() after the uring Dispatcher is started to establish the link.
func New(fsSID int) *Client {
	return &Client{
		fsSID:  fsSID,
		RespCh: make(chan any, 4),
	}
}

// DecodeResp is the uring Dispatcher decoder for ProtoFSIPCResp messages.
func DecodeResp(msg *ipc.UringIPCMsg) any {
	p := ipc.DecodeFSIPCResp(msg)
	return *p
}

// Connect allocates the shared data area, maps it into fs, and sends the
// handshake message. Blocks until fs confirms the connection.
func (c *Client) Connect() error {
	// Allocate shared data pages.
	ptr, err := mem.AllocPages(dataPages, mem.PageIPC)
	if err != nil {
		return fmt.Errorf("fsclient: AllocPages: %w", err)
	}
	c.localVA = uintptr(ptr)
	c.dataLen = dataPages * 4096

	// Map into fs's address space.
	remote, mapErr := sys.SharePages(c.fsSID, c.localVA)
	if mapErr != nil {
		return fmt.Errorf("fsclient: SharePages: %w", mapErr)
	}
	c.remoteVA = remote

	// Send connect handshake.
	resp, err := c.call(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpConnect,
		DataVA:  uint64(c.remoteVA),
		DataLen: uint32(c.dataLen),
	})
	if err != nil {
		return err
	}
	if resp.Err != 0 {
		return fmt.Errorf("fsclient: connect rejected: %d", resp.Err)
	}
	return nil
}

// call sends a request and blocks for the response.
func (c *Client) call(req *ipc.FSIPCReqPayload) (ipc.FSIPCRespPayload, error) {
	c.nextID++
	req.ReqID = c.nextID
	if req.DataVA == 0 {
		req.DataVA = uint64(c.remoteVA)
	}

	msg := ipc.EncodeFSIPCReq(req, int16(os.Getpid()))
	if err := uring.Send(c.fsSID, &msg); err != nil {
		return ipc.FSIPCRespPayload{}, fmt.Errorf("fsclient: Send: %w", err)
	}

	raw := <-c.RespCh
	resp, ok := raw.(ipc.FSIPCRespPayload)
	if !ok {
		return ipc.FSIPCRespPayload{}, errors.New("fsclient: unexpected response type")
	}
	return resp, nil
}

// dataArea returns the local shared data area as a byte slice.
func (c *Client) dataArea() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(c.localVA)), c.dataLen)
}

// setPath writes a null-terminated path to the start of the data area,
// returns the length including the null terminator.
func (c *Client) setPath(path string) uint16 {
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
// Returns (handle, ftype, size, err). ftype: 1=file, 2=dir.
func (c *Client) Open(path string, flags, mode uint32) (handle uint32, ftype uint8, size uint32, err error) {
	pathLen := c.setPath(path)
	resp, callErr := c.call(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpOpen,
		PathLen: pathLen,
		Flags:   flags,
		Mode:    mode,
	})
	if callErr != nil {
		return 0, 0, 0, callErr
	}
	if resp.Err != 0 {
		return 0, 0, 0, ErrnoErr(resp.Err)
	}
	return uint32(resp.Result0), uint8(resp.Result1 >> 32), uint32(resp.Result1 & 0xFFFFFFFF), nil
}

// Close closes a handle.
func (c *Client) Close(handle uint32) error {
	resp, err := c.call(&ipc.FSIPCReqPayload{
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

// Read reads up to count bytes from handle at offset into buf.
// Returns the number of bytes read. Data is in the shared data area;
// the caller must copy it out before the next IPC call.
func (c *Client) Read(handle uint32, offset int64, count int) (int, error) {
	if count > c.dataLen {
		count = c.dataLen
	}
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
	return int(resp.DataLen), nil
}

// DataSlice returns a slice of the first n bytes from the shared data area.
// Used after Read/Stat/ReadDir to access response data. The caller must
// copy the data before the next IPC call.
func (c *Client) DataSlice(n int) []byte {
	area := c.dataArea()
	if n > len(area) {
		n = len(area)
	}
	return area[:n]
}

// WriteData copies data into the shared data area for write requests.
// Returns the number of bytes copied.
func (c *Client) WriteData(data []byte) int {
	area := c.dataArea()
	n := len(data)
	if n > len(area) {
		n = len(area)
	}
	copy(area[:n], data[:n])
	return n
}

// Write writes data to handle at offset. Caller must call WriteData first.
func (c *Client) Write(handle uint32, offset int64, dataLen int) (int, error) {
	resp, err := c.call(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpWrite,
		Handle:  handle,
		Arg0:    uint64(offset),
		DataLen: uint32(dataLen),
	})
	if err != nil {
		return 0, err
	}
	if resp.Err != 0 {
		return 0, ErrnoErr(resp.Err)
	}
	return int(resp.Result0), nil
}

// Stat stats a path. Result is in the shared data area (128 bytes).
func (c *Client) Stat(path string) (int32, error) {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
		Op:      ipc.FSOpStat,
		PathLen: pathLen,
	})
	if err != nil {
		return 0, err
	}
	return resp.Err, nil
}

// Fstat stats a handle. Result is in the shared data area (128 bytes).
func (c *Client) Fstat(handle uint32) (int32, error) {
	resp, err := c.call(&ipc.FSIPCReqPayload{
		Op:     ipc.FSOpFstat,
		Handle: handle,
	})
	if err != nil {
		return 0, err
	}
	return resp.Err, nil
}

// Mkdir creates a directory.
func (c *Client) Mkdir(path string, mode uint32) error {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) Rename(oldPath, newPath string) error {
	area := c.dataArea()
	// Pack: oldpath\0newpath\0
	n := copy(area, oldPath)
	area[n] = 0
	newOff := n + 1
	n2 := copy(area[newOff:], newPath)
	area[newOff+n2] = 0
	totalLen := uint32(newOff + n2 + 1)

	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) Remove(path string) error {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
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

// ReadDir reads directory entries. Data is in shared data area.
// Returns (dataLen, entryCount, err).
func (c *Client) ReadDir(handle uint32, startIdx int) (int, int, error) {
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
	return int(resp.DataLen), int(resp.Result0), nil
}

// Access checks if a path exists.
func (c *Client) Access(path string) error {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) SetMode(path string, mode uint32) error {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) SetTimes(path string) error {
	pathLen := c.setPath(path)
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) Truncate(handle uint32, size int64) error {
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) Sync() error {
	resp, err := c.call(&ipc.FSIPCReqPayload{
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
func (c *Client) Resolve(path string) (isDir bool, size uint32, err error) {
	pathLen := c.setPath(path)
	resp, callErr := c.call(&ipc.FSIPCReqPayload{
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
func IsErrno(err error) (int32, bool) {
	if e, ok := err.(ErrnoErr); ok {
		return e.Errno(), true
	}
	return 0, false
}
