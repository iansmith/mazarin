package uring

import (
	"mazzy/shared/ipc"
	_ "unsafe"
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
	ringIdx int
	done    chan struct{}
}

// NewReader creates a new uring reader on ring 0. Call Start() to begin receiving.
func NewReader(handler Handler) *Reader {
	return NewReaderWithRing(handler, 0)
}

// NewReaderWithRing creates a new uring reader on the specified ring index.
func NewReaderWithRing(handler Handler, ringIdx int) *Reader {
	return &Reader{
		handler: handler,
		ringIdx: ringIdx,
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
		err := RecvWithRing(&msg, r.ringIdx)
		if err != nil {
			uartPuts("[uring:reader] Recv error, exiting loop\n")
			return
		}
		r.handler(&msg)
	}
}

//go:linkname uartWriteString mazzy/mazarin/sys.UartWriteString
func uartWriteString(s string)

func uartPuts(s string) { uartWriteString(s) }

func uartPutsInt(n int) {
	if n == 0 {
		uartPuts("0")
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	uartPuts(string(buf[i:]))
}

// Done returns a channel that is closed when the reader goroutine exits.
func (r *Reader) Done() <-chan struct{} {
	return r.done
}

// Decoder converts a raw UringIPCMsg into a typed struct.
// Each protocol has its own decoder (e.g., wm.DecodeWMNotify).
type Decoder func(msg *ipc.UringIPCMsg) any

// typedRoute pairs a decoder with its destination (channel or callback).
type typedRoute struct {
	decoder  Decoder
	ch       chan any  // nil if using callback
	callback func(any) // nil if using channel
}

// DeathHandler is called when a peer shepherd dies. The deadSID identifies
// which shepherd terminated. The handler runs inside defer/recover — even if
// it panics, the uring reader continues and Release is guaranteed.
type DeathHandler func(deadSID int16)

// Dispatcher routes incoming uring messages to protocol-specific Go channels
// or callback functions. Each protocol is registered with a Decoder function
// that converts the raw 128-byte message into a typed Go struct.
// Unregistered protocols are logged and dropped (no panic).
type Dispatcher struct {
	reader       *Reader
	ringIdx      int
	routes       map[uint32]typedRoute
	deathHandler DeathHandler
}

// NewDispatcher creates a dispatcher on ring 0 that routes messages by protocol.
// Register protocol channels with On() before calling Start().
func NewDispatcher() *Dispatcher {
	return NewDispatcherWithRing(0)
}

// NewDispatcherWithRing creates a dispatcher on the specified ring index.
func NewDispatcherWithRing(ringIdx int) *Dispatcher {
	return &Dispatcher{
		ringIdx: ringIdx,
		routes:  make(map[uint32]typedRoute),
	}
}

// On registers a typed channel for a specific protocol. The decoder converts
// the raw UringIPCMsg into a typed struct; the result is sent on ch.
// The channel should be buffered to avoid blocking the reader goroutine.
func (d *Dispatcher) On(protocol uint32, decoder Decoder, ch chan any) {
	d.routes[protocol] = typedRoute{decoder: decoder, ch: ch}
}

// OnFunc registers a callback function for a specific protocol. The decoder
// converts the raw UringIPCMsg into a typed struct; the result is passed
// directly to the callback (runs synchronously in the reader goroutine).
func (d *Dispatcher) OnFunc(protocol uint32, decoder Decoder, fn func(any)) {
	d.routes[protocol] = typedRoute{decoder: decoder, callback: fn}
}

// OnDeath registers a handler for ProtoDeath notifications. The handler is
// called with the dead shepherd's SID. It runs inside defer/recover so the
// reader goroutine continues even if the handler panics. Connection cleanup
// (Release) should be done by the handler.
func (d *Dispatcher) OnDeath(fn DeathHandler) {
	d.deathHandler = fn
}

// Start spawns the reader goroutine with protocol-based typed dispatch.
func (d *Dispatcher) Start() {
	// Snapshot the route map and death handler so the handler closure
	// doesn't need synchronization.
	routes := make(map[uint32]typedRoute, len(d.routes))
	for k, v := range d.routes {
		routes[k] = v
	}
	deathFn := d.deathHandler

	d.reader = NewReaderWithRing(func(msg *ipc.UringIPCMsg) {
		// Handle ProtoDeath with defer/recover to guarantee the reader
		// continues even if the death handler panics.
		if msg.Protocol == ipc.ProtoDeath {
			if deathFn != nil {
				dn := ipc.DecodeDeathNotification(msg)
				func() {
					defer func() {
						if r := recover(); r != nil {
							uartPuts("[uring:dispatch] death handler panic: ")
							uartPuts("\n")
						}
					}()
					deathFn(dn.DeadSID)
				}()
			}
			return
		}

		route, ok := routes[msg.Protocol]
		if !ok {
			uartPuts("[uring:dispatch] unknown proto=")
			uartPutsInt(int(msg.Protocol))
			uartPuts(", dropping\n")
			return
		}
		typed := route.decoder(msg)
		if route.callback != nil {
			route.callback(typed)
		} else {
			route.ch <- typed
		}
	}, d.ringIdx)
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
