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

	uartPuts("[uring:reader] loop started, calling Recv...\n")
	var msg ipc.UringIPCMsg
	for {
		err := Recv(&msg)
		if err != nil {
			uartPuts("[uring:reader] Recv error, exiting loop\n")
			return
		}
		uartPuts("[uring:reader] got msg proto=")
		uartPutsInt(int(msg.Protocol))
		uartPuts("\n")
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

// Dispatcher routes incoming uring messages to protocol-specific Go channels
// or callback functions. Each protocol is registered with a Decoder function
// that converts the raw 128-byte message into a typed Go struct.
// The dispatcher panics on messages with unregistered protocols.
type Dispatcher struct {
	reader *Reader
	routes map[uint32]typedRoute
}

// NewDispatcher creates a dispatcher that routes messages by protocol.
// Register protocol channels with On() before calling Start().
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		routes: make(map[uint32]typedRoute),
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

// Start spawns the reader goroutine with protocol-based typed dispatch.
func (d *Dispatcher) Start() {
	// Snapshot the route map so the handler doesn't need synchronization.
	routes := make(map[uint32]typedRoute, len(d.routes))
	for k, v := range d.routes {
		routes[k] = v
	}

	d.reader = NewReader(func(msg *ipc.UringIPCMsg) {
		route, ok := routes[msg.Protocol]
		if !ok {
			panic("uring.Dispatcher: unregistered protocol")
		}
		typed := route.decoder(msg)
		if route.callback != nil {
			route.callback(typed)
		} else {
			route.ch <- typed
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
