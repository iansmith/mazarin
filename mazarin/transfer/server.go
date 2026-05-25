package transfer

import (
	"os"
	"sync"
	"time"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

// DefaultCommitTimeout is how long Slab.Wait blocks before returning
// ErrCommitTimeout. Caller can override via Slab.WaitTimeout for per-slab
// control (e.g. integration tests that intentionally don't Commit).
const DefaultCommitTimeout = 30 * time.Second

// Server is the server-side Mode 1 endpoint. Holds the in-flight Slab table
// and feeds newly-Reserved Slabs to the server's main loop via NewSlabs.
type Server struct {
	newSlabs chan *Slab // bounded buffer; main loop reads to pick up Reserved Slabs

	mu      sync.Mutex
	pending map[pendingKey]*Slab // key = (clientSID, reqID); value = the Slab awaiting Commit
}

type pendingKey struct {
	clientSID ShepherdID
	reqID     uint32
}

// NewServer returns a Server with a buffered NewSlabs channel.
// bufSize controls back-pressure on Reserve handling — pick large enough that
// the main loop can keep up with bursts.
func NewServer(bufSize int) *Server {
	if bufSize < 1 {
		bufSize = 16
	}
	return &Server{
		newSlabs: make(chan *Slab, bufSize),
		pending:  make(map[pendingKey]*Slab),
	}
}

// NewSlabs returns the channel on which freshly-Reserved Slabs arrive.
// The main loop calls slab.Wait() then processes slab.Bytes() then
// slab.Release(). Closing the dispatcher closes this channel.
func (s *Server) NewSlabs() <-chan *Slab {
	return s.newSlabs
}

// taggedReq pairs a decoded request with the sender's SID — the dispatcher
// callback signature drops msg.SenderSID, so we capture it at decode time.
type taggedReq struct {
	payload   ipc.TransferReqPayload
	senderSID ShepherdID
}

func decodeReqTagged(msg *ipc.UringIPCMsg) any {
	return taggedReq{
		payload:   *ipc.DecodeTransferReq(msg),
		senderSID: ShepherdID(msg.SenderSID),
	}
}

// RegisterDispatch wires the Server's handler into a uring.Dispatcher.
// Call before dispatcher.Start().
func (s *Server) RegisterDispatch(d *uring.Dispatcher) {
	d.OnFunc(ipc.ProtoTransferReq, decodeReqTagged, s.handle)
}

func (s *Server) handle(v any) {
	tr := v.(taggedReq)
	switch tr.payload.Op {
	case ipc.TransferOpReserve:
		s.handleReserve(tr)
	case ipc.TransferOpCommit:
		s.handleCommit(tr)
	}
}

func (s *Server) handleReserve(tr taggedReq) {
	slab, err := Allocate(int(tr.payload.Size), 0)
	if err != nil {
		s.sendErrorResp(tr.senderSID, tr.payload.ReqID, -22) // EINVAL
		return
	}
	// Transfer all the Slab's pages to the client. extraPages == 0 in v1,
	// so the full allocation is the body region.
	clientVA, err := sys.TransferPages(int(tr.senderSID), slab.va, slab.pages, 0)
	if err != nil {
		slab.Release()
		s.sendErrorResp(tr.senderSID, tr.payload.ReqID, -14) // EFAULT
		return
	}
	slab.state = slabMappedToClient

	key := pendingKey{tr.senderSID, tr.payload.ReqID}
	s.mu.Lock()
	s.pending[key] = slab
	s.mu.Unlock()

	resp := ipc.TransferRespPayload{
		ReqID: tr.payload.ReqID,
		VA:    uint64(clientVA),
		Pages: uint32(slab.pages),
	}
	respMsg := ipc.EncodeTransferResp(&resp, int16(os.Getpid()))
	_ = uring.Send(int(tr.senderSID), &respMsg)

	// Hand the Slab to the server's main loop. Drop if the buffer is full —
	// the main loop is too slow and the Slab would otherwise leak in pending.
	// Caller chooses bufSize at NewServer time to size against expected burst.
	select {
	case s.newSlabs <- slab:
	default:
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		slab.Release()
	}
}

func (s *Server) handleCommit(tr taggedReq) {
	key := pendingKey{tr.senderSID, tr.payload.ReqID}
	s.mu.Lock()
	slab, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	// Client's TransferAndUnmap returned the new server-side VA in req.VA.
	// Body pages now live at that VA in this server's address space.
	slab.va = uintptr(tr.payload.VA)
	slab.state = slabCommitted
	select {
	case slab.commitCh <- nil:
	default:
	}
}

func (s *Server) sendErrorResp(clientSID ShepherdID, reqID uint32, errno int32) {
	resp := ipc.TransferRespPayload{ReqID: reqID, Err: errno}
	msg := ipc.EncodeTransferResp(&resp, int16(os.Getpid()))
	_ = uring.Send(int(clientSID), &msg)
}

// Wait blocks until the matching Commit arrives or the timeout fires.
// Returns nil on success; ErrCommitTimeout on timeout (likely client crash).
// Uses DefaultCommitTimeout; see WaitTimeout for explicit control.
func (s *Slab) Wait() error {
	return s.WaitTimeout(DefaultCommitTimeout)
}

// WaitTimeout is Wait with an explicit timeout.
func (s *Slab) WaitTimeout(timeout time.Duration) error {
	if s.state == slabCommitted {
		return nil
	}
	select {
	case err := <-s.commitCh:
		return err
	case <-time.After(timeout):
		s.state = slabReleased // kernel reclaimed the orphaned mapping
		return ErrCommitTimeout
	}
}
