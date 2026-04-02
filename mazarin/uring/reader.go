package uring

import (
	"mazzy/shared/ipc"
)

// Handler is called for each received message. Implementations should
// switch on msg.Protocol to dispatch to the appropriate handler.
type Handler func(msg *ipc.UringIPCMsg)

// Reader is a dedicated goroutine that loops on SysUringRecv and dispatches
// received messages to a handler function. When a message arrives, the
// thread wakes via exitsyscall (active P acquisition), ensuring immediate
// dispatch without depending on sysmon's retake polling.
type Reader struct {
	handler Handler
	done    chan struct{}
}

// NewReader creates a new uring reader. Call Start() to begin receiving.
func NewReader(handler Handler) *Reader {
	return &Reader{
		handler: handler,
		done:    make(chan struct{}),
	}
}

// Start spawns the reader goroutine. It loops on SysUringRecv, calling
// the handler for each message. The goroutine runs until the shepherd
// exits or the uring ring is torn down by the kernel.
//
// The reader goroutine will acquire its own M via the Go scheduler.
// When SysUringRecv returns (message received), exitsyscall actively
// acquires a P — this is the key fix for the P-scheduling stall.
func (r *Reader) Start() {
	go r.loop()
}

func (r *Reader) loop() {
	defer close(r.done)

	var msg ipc.UringIPCMsg
	for {
		err := Recv(&msg)
		if err != nil {
			// Kernel returned error — ring torn down or shepherd dying.
			return
		}
		r.handler(&msg)
	}
}

// Done returns a channel that is closed when the reader goroutine exits.
func (r *Reader) Done() <-chan struct{} {
	return r.done
}

// Dispatcher is a convenience wrapper around Reader that routes messages
// to protocol-specific Go channels based on msg.Protocol.
type Dispatcher struct {
	reader   *Reader
	channels map[uint32]chan ipc.UringIPCMsg
	fallback chan ipc.UringIPCMsg
}

// NewDispatcher creates a dispatcher that routes messages by protocol.
// Register protocol channels with On() before calling Start().
// Messages with unregistered protocols go to the fallback channel (if set).
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		channels: make(map[uint32]chan ipc.UringIPCMsg),
	}
}

// On registers a channel for a specific protocol. Messages with the given
// protocol will be sent to this channel. The channel should be buffered
// to avoid blocking the reader goroutine.
func (d *Dispatcher) On(protocol uint32, ch chan ipc.UringIPCMsg) {
	d.channels[protocol] = ch
}

// OnFallback sets a channel for messages with unregistered protocols.
func (d *Dispatcher) OnFallback(ch chan ipc.UringIPCMsg) {
	d.fallback = ch
}

// Start spawns the reader goroutine with protocol-based dispatch.
func (d *Dispatcher) Start() {
	// Snapshot the channel map so the handler doesn't need synchronization.
	chMap := make(map[uint32]chan ipc.UringIPCMsg, len(d.channels))
	for k, v := range d.channels {
		chMap[k] = v
	}
	fb := d.fallback

	d.reader = NewReader(func(msg *ipc.UringIPCMsg) {
		if ch, ok := chMap[msg.Protocol]; ok {
			ch <- *msg
		} else if fb != nil {
			fb <- *msg
		}
	})
	d.reader.Start()
}

// Done returns a channel that is closed when the reader goroutine exits.
func (d *Dispatcher) Done() <-chan struct{} {
	if d.reader == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return d.reader.Done()
}
