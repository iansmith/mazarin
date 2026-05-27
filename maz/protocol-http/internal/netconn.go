// netconn.go — net.Conn adapter over mazarin/netclient's page-loan stream
// socket. Lifted from maz/sophie/netconn.go in MAZ-49 item 3, with the
// type and constructor exported so the protocol-http shepherd can use
// them under their own package name.
//
// Read buffers partial StreamChunks; returns io.EOF on the kernel's EOF
// chunk (Page=0, Length=0, EOF=true) without ReleaseRX. Write splits
// payloads larger than MaxStreamSendSize. Close half-closes the write
// side via Shutdown then tears down with Close. SetDeadline / addrs are
// stubs — crypto/tls doesn't call them on the one-shot Do path.
//
// Not safe for concurrent Read/Write/Close from multiple goroutines.
// protocol-http processes one Do request at a time per connection in
// v1; if multi-request concurrency arrives, promote `closed` to
// atomic.Bool and consider per-conn serialization at the dispatcher.
package internal

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"mazzy/mazarin/netclient"
	"mazzy/shared/netproto"
)

// Conn adapts a netclient.NetClient stream to net.Conn. Construct one
// per TCP connection via New.
type Conn struct {
	nc      netclient.NetClient
	connID  uint32
	rxBuf   []byte
	eofSeen bool
	closed  bool
}

// New wraps a netclient stream connection (connID) so crypto/tls can
// drive it via the net.Conn interface.
func New(nc netclient.NetClient, connID uint32) *Conn {
	return &Conn{nc: nc, connID: connID}
}

// Read fills p from the buffered tail of the last chunk, then fetches
// more chunks as needed. Returns io.EOF once the kernel's EOF chunk has
// been drained.
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.rxBuf) > 0 {
		n := copy(p, c.rxBuf)
		c.rxBuf = c.rxBuf[n:]
		return n, nil
	}
	if c.eofSeen {
		return 0, io.EOF
	}
	chunk, err := c.nc.ReadStream(c.connID)
	if err != nil {
		return 0, err
	}
	if chunk.EOF {
		c.eofSeen = true
		return 0, io.EOF
	}
	// ORDERING INVARIANT: stash the remainder BEFORE ReleaseRX. payload
	// aliases the page-loan memory; once we ReleaseRX, net.elf may reuse
	// the page and overwrite those bytes.
	payload := chunk.Payload()
	n := copy(p, payload)
	if n < len(payload) {
		c.rxBuf = append(c.rxBuf[:0], payload[n:]...)
	}
	if err := c.nc.ReleaseRX(c.connID, chunk.Page); err != nil {
		return n, fmt.Errorf("netconn: ReleaseRX: %w", err)
	}
	return n, nil
}

// Write splits payload across StreamSend's per-call cap and returns the
// total bytes accepted by net.elf across all sub-sends.
func (c *Conn) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("netconn: write on closed conn")
	}
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > netclient.MaxStreamSendSize {
			chunk = chunk[:netclient.MaxStreamSendSize]
		}
		sent, err := c.nc.StreamSend(c.connID, chunk)
		total += sent
		if err != nil {
			return total, err
		}
		// net.elf may accept less than we offered; resend the tail.
		p = p[sent:]
	}
	return total, nil
}

// Close half-closes the write side then tears down. Idempotent.
func (c *Conn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	// Ignore shutdown errors — Close still runs and is the authoritative
	// teardown. A failed half-close is informational at best.
	_ = c.nc.Shutdown(c.connID, netproto.ShutdownRead|netproto.ShutdownWrite)
	return c.nc.Close(c.connID)
}

// LocalAddr / RemoteAddr return placeholder addrs. crypto/tls doesn't
// read them on the hot path but the net.Conn interface requires
// non-nil returns.
func (c *Conn) LocalAddr() net.Addr  { return &net.IPAddr{IP: net.IPv4zero} }
func (c *Conn) RemoteAddr() net.Addr { return &net.IPAddr{IP: net.IPv4zero} }

// SetDeadline / SetReadDeadline / SetWriteDeadline are no-ops — crypto/tls
// only invokes these if the user calls SetDeadline on the *tls.Conn,
// which protocol-http's hot path does not.
func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

// Compile-time check that *Conn satisfies net.Conn.
var _ net.Conn = (*Conn)(nil)
