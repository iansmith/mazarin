// netconn.go — net.Conn adapter over mazarin/netclient's page-loan
// stream socket. Wraps a netclient.NetClient + connID pair so that
// crypto/tls and net/http can treat it like a regular TCP conn.
//
// Read buffers partial StreamChunks; returns io.EOF on the kernel's
// EOF chunk (Page=0, Length=0, EOF=true) without ReleaseRX (Page=0
// is invalid). Write splits payloads larger than MaxStreamSendSize.
// Close half-closes the write side via Shutdown then tears down with
// Close. SetDeadline / addrs are stubs — crypto/tls doesn't call them
// for sophie's one-shot usage.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"mazzy/mazarin/netclient"
	"mazzy/shared/netproto"
)

// netconn adapts a netclient.NetClient stream to net.Conn. Single-conn
// (one connID); construct a new netconn per TCP connection. Not safe
// for concurrent Read/Write/Close from multiple goroutines — sophie's
// one-shot Ask path is single-goroutine, and that's the only intended
// use. Promote `closed` to atomic.Bool if a future caller breaks this.
type netconn struct {
	nc      netclient.NetClient
	connID  uint32
	rxBuf   []byte // bytes left over from the last ReadStream chunk
	eofSeen bool
	closed  bool
}

func newNetConn(nc netclient.NetClient, connID uint32) *netconn {
	return &netconn{nc: nc, connID: connID}
}

// Read fills p from the buffered tail of the last chunk, then fetches
// more chunks as needed. Returns io.EOF after the kernel's EOF chunk
// has been drained.
func (n *netconn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(n.rxBuf) > 0 {
		c := copy(p, n.rxBuf)
		n.rxBuf = n.rxBuf[c:]
		return c, nil
	}
	if n.eofSeen {
		return 0, io.EOF
	}
	chunk, err := n.nc.ReadStream(n.connID)
	if err != nil {
		return 0, err
	}
	if chunk.EOF {
		// EOF chunk: Page=0, no ReleaseRX. If we had buffered bytes we'd
		// have returned them on a prior call; here we report end-of-stream.
		n.eofSeen = true
		return 0, io.EOF
	}
	// Copy the page-loan bytes into our own buffer so we can ReleaseRX
	// immediately. crypto/tls retains pointers to Read's destination
	// slice, not to our internal buffer, so this copy is the only safe
	// shape for releasing the page back to net.elf.
	//
	// ORDERING INVARIANT: stash the remainder BEFORE ReleaseRX. payload
	// aliases the page-loan memory; once we ReleaseRX, net.elf may
	// reuse the page and overwrite those bytes.
	payload := chunk.Payload()
	c := copy(p, payload)
	if c < len(payload) {
		n.rxBuf = append(n.rxBuf[:0], payload[c:]...)
	}
	if err := n.nc.ReleaseRX(n.connID, chunk.Page); err != nil {
		// Surface as an error — we can't ReleaseRX, so the page leaks.
		// Better to fail loudly than to silently leak descriptor pages.
		return c, fmt.Errorf("netconn: ReleaseRX: %w", err)
	}
	return c, nil
}

// Write splits payload across StreamSend's per-call cap and returns the
// total bytes accepted by gvisor across all sub-sends.
func (n *netconn) Write(p []byte) (int, error) {
	if n.closed {
		return 0, errors.New("netconn: write on closed conn")
	}
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > netclient.MaxStreamSendSize {
			chunk = chunk[:netclient.MaxStreamSendSize]
		}
		sent, err := n.nc.StreamSend(n.connID, chunk)
		total += sent
		if err != nil {
			return total, err
		}
		// gvisor may accept less than we offered; resend the tail.
		p = p[sent:]
	}
	return total, nil
}

// Close half-closes the write side then tears down. The half-close
// flushes any buffered write data and signals end-of-stream to the
// peer; the Close releases the stream conn back to net.elf.
func (n *netconn) Close() error {
	if n.closed {
		return nil
	}
	n.closed = true
	// Ignore shutdown errors — Close still runs and is the authoritative
	// teardown. A failed half-close is informational at best.
	_ = n.nc.Shutdown(n.connID, netproto.ShutdownRead|netproto.ShutdownWrite)
	return n.nc.Close(n.connID)
}

// LocalAddr / RemoteAddr return placeholder addrs. crypto/tls and
// net/http don't read them on sophie's hot path, but the net.Conn
// interface requires non-nil returns.
func (n *netconn) LocalAddr() net.Addr  { return &net.IPAddr{IP: net.IPv4zero} }
func (n *netconn) RemoteAddr() net.Addr { return &net.IPAddr{IP: net.IPv4zero} }

// SetDeadline / SetReadDeadline / SetWriteDeadline are no-ops.
// crypto/tls only invokes these if the user calls SetDeadline on the
// *tls.Conn, which sophie does not.
func (n *netconn) SetDeadline(time.Time) error      { return nil }
func (n *netconn) SetReadDeadline(time.Time) error  { return nil }
func (n *netconn) SetWriteDeadline(time.Time) error { return nil }

// Compile-time check that netconn satisfies net.Conn.
var _ net.Conn = (*netconn)(nil)
