package main

import (
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/fs/fsipc"
)

// fsIPCClient manages the linux shepherd's side of the linux↔fs IPC channel.
// The delegate handler goroutine calls call() to send requests; the mailbox
// recv goroutine delivers responses via respCh.
type fsIPCClient struct {
	fsSID    int
	reqRing  *ringbuf.RingBuffer
	respRing *ringbuf.RingBuffer // set after handshake
	dataArea []byte              // shared data (in linux's VA space), set after handshake
	respCh   chan fsipc.Response
	readyCh  chan struct{} // closed when handshake completes
	nextID   uint64
}

// newFsIPCClient creates the request ring and prepares the handshake.
// Call sendInit() after the mailbox recv goroutine is running to complete setup.
func newFsIPCClient(fsSID int) *fsIPCClient {
	c := &fsIPCClient{
		fsSID:   fsSID,
		respCh:  make(chan fsipc.Response, 4),
		readyCh: make(chan struct{}),
	}

	var err error
	c.reqRing, err = ringbuf.New(fsSID, 0, fsipc.SlotSize, fsipc.SlotCount)
	if err != nil {
		sys.UartWriteString("[linux] IPC: ringbuf.New failed: " + err.Error() + "\n")
		return c
	}

	return c
}

// sendInit pushes the handshake message and notifies fs.
// Must be called after the mailbox recv goroutine is running.
func (c *fsIPCClient) sendInit() {
	if c.reqRing == nil {
		return
	}
	var req fsipc.Request
	req.Op = fsipc.OpHandshake
	c.reqRing.Push(unsafe.Pointer(&req))
	if err := sys.MailboxSend(c.fsSID, fsipc.NotifyInit, c.reqRing.Addr()); err != nil {
		sys.UartWriteString("[linux] IPC: MailboxSend NotifyInit failed: " + err.Error() + "\n")
	}
}

// handleReady processes the NotifyReady notification from fs.
// Called by the mailbox recv goroutine.
func (c *fsIPCClient) handleReady(notif sys.MailboxNotification) {
	// Open the response ring at the translated VA.
	c.respRing = ringbuf.Open(uintptr(notif.RingAddr))

	// Pop the handshake ack to get the data area VA.
	var ack fsipc.Response
	if c.respRing.Pop(unsafe.Pointer(&ack)) {
		dataVA := uintptr(ack.Result1)
		dataLen := int(ack.DataLen)
		if dataLen > 0 {
			c.dataArea = unsafe.Slice((*byte)(unsafe.Pointer(dataVA)), dataLen)
		}
	}

	close(c.readyCh)
	sys.UartWriteString("[linux] IPC handshake complete\n")
}

// handleResponse processes a NotifyResponse notification from fs.
// Pops all responses from the ring and delivers them on respCh.
// Called by the mailbox recv goroutine.
func (c *fsIPCClient) handleResponse() {
	if c.respRing == nil {
		sys.UartWriteString("[linux:ipc] handleResponse: respRing nil!\n")
		return
	}
	var resp fsipc.Response
	for c.respRing.Pop(unsafe.Pointer(&resp)) {
		c.respCh <- resp
	}
}

// ready returns true if the handshake is complete and IPC is usable.
func (c *fsIPCClient) ready() bool {
	select {
	case <-c.readyCh:
		return true
	default:
		return false
	}
}

// call sends a request to the fs shepherd and blocks until the response arrives.
// Must only be called from the delegate handler goroutine.
func (c *fsIPCClient) call(req *fsipc.Request) fsipc.Response {
	c.nextID++
	req.ID = c.nextID
	c.reqRing.Push(unsafe.Pointer(req))
	err := sys.MailboxSend(c.fsSID, fsipc.NotifyRequest, c.reqRing.Addr())
	if err != nil {
		sys.UartWriteString("[linux:ipc] MailboxSend FAILED\n")
	}
	return <-c.respCh
}

// dataSlice returns the shared data area for reading response data.
// The caller must copy out the data before the next IPC call.
func (c *fsIPCClient) dataSlice(n int) []byte {
	if n > len(c.dataArea) {
		n = len(c.dataArea)
	}
	return c.dataArea[:n]
}

// writeData copies data into the shared data area for write requests.
func (c *fsIPCClient) writeData(data []byte) int {
	n := len(data)
	if n > len(c.dataArea) {
		n = len(c.dataArea)
	}
	copy(c.dataArea[:n], data[:n])
	return n
}

// allocDataPage is used during handshake to allocate a data page.
// This is called internally, not exported.
func allocDataPage(targetSID int) (localVA uintptr, remoteVA uintptr, err error) {
	page, allocErr := mem.AllocPages(1, mem.PageIPC)
	if allocErr != nil {
		return 0, 0, allocErr
	}
	*(*byte)(unsafe.Pointer(uintptr(page))) = 0
	remote, mapErr := sys.MailboxMapPage(targetSID, uintptr(page))
	if mapErr != nil {
		return 0, 0, mapErr
	}
	return uintptr(page), remote, nil
}
