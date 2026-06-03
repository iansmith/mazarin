// Package pipe holds the linux shepherd's Unix-pipe data plane.
//
// A pipe is a unidirectional byte channel with a bounded buffer. pipe2(2)
// returns a linked (read-end, write-end) pair: bytes written to the write
// end become readable on the read end, in FIFO order. When all write ends
// are closed and the buffer is drained, reads return EOF (0 bytes).
//
// Why this lives in `internal/`: maz/linux's main package is `package main`
// (a shepherd binary), which makes unit-testing painful. Lifting the pipe
// data plane into an internal package keeps the linux shepherd thin and
// gives the logic a host-testable home — matching the pattern at
// `maz/linux/internal/processrecord/` and `maz/protocol-http/internal/`.
//
// The shepherd-side fdEntry (owned by the FD-table cluster: MAZ-122/119/113)
// wraps an *End here; sysPipe2 / pipe-backed read+write delegate into this
// package. The O_CLOEXEC bit on the resulting fdEntry is set from each End's
// Cloexec flag — this package is the single source of truth for the flag at
// pipe-creation time, consistent with MAZ-122's end-to-end O_CLOEXEC contract.
//
// Concurrency: a Pipe is NOT goroutine-safe on its own. The linux shepherd's
// per-shepherd handler holds ShepherdFilesystemData.mu for the lifetime of a
// dispatch, which serializes access. Blocking reads are handled by the
// shepherd's deferred-Reply (park) mechanism, NOT by blocking inside this
// package — Read reports "would block" and the caller parks the request.

package pipe

import "errors"

// O_CLOEXEC / O_NONBLOCK as seen on the pipe2 flags argument. Linux ARM64 and
// x86_64 share these values (asm-generic + x86 agree).
const (
	OCLOEXEC  = 0x80000
	ONONBLOCK = 0x800
)

// bufCap bounds a single pipe's buffer. The fork/exec consumers this ticket
// feeds (cmd.Stdout capture, the errpipe success protocol) carry at most a few
// bytes, so one page is ample; capping at a page keeps shepherd memory in
// check. Linux's default is 64 KiB — revisit if a pipe-heavy consumer appears.
const bufCap = 4096

// ErrWouldBlock is returned by Read when the buffer is empty but write ends
// remain open, and by Write when a non-blocking write hits a full buffer.
// The shepherd translates this into either EAGAIN (O_NONBLOCK) or a parked
// request (blocking), per the deferred-Reply model.
var ErrWouldBlock = errors.New("pipe: operation would block")

// buf is the shared FIFO byte buffer behind a (read, write) End pair. Both
// ends point at the same *buf; the read End drains it, the write End fills it.
// writers counts open write ends so Read can distinguish "no data yet" (block)
// from "no data ever again" (EOF). flags is the pipe2 creation flags argument.
//
// waiters holds opaque tokens for read requests the shepherd has parked on an
// empty pipe (one per blocked reader). The pipe package never inspects a token;
// it just hands them back from TakeWaiters once the pipe becomes readable or
// hits EOF, so the shepherd can fulfill the corresponding parked Reply. This
// keeps "who is blocked on this buffer" inside the package (the shared buf is
// the natural owner) without the package knowing what a shepherd request is.
type buf struct {
	data    []byte
	writers int
	flags   int
	waiters []any
}

// End is one end of a pipe — either the read end or the write end. Both ends
// share the same underlying buffer. isWriter distinguishes the two so Close on
// the write end can decrement the shared writer count. The Cloexec / Nonblock
// flags are derived from the pipe2 flags argument (stored on the shared buf)
// and apply to both ends.
type End struct {
	b        *buf
	isWriter bool
	closed   bool
}

// New creates a linked (read, write) End pair sharing a fresh buffer. flags is
// the pipe2(2) flags argument (O_CLOEXEC / O_NONBLOCK); both ends inherit the
// derived Cloexec / Nonblock flags. The pair starts with one open writer.
func New(flags int) (read *End, write *End) {
	b := &buf{writers: 1, flags: flags}
	return &End{b: b}, &End{b: b, isWriter: true}
}

// Fork creates a new End that shares the same underlying pipe buffer as e,
// modeling Linux fork(2) FD inheritance: the child gets an independent End
// struct pointing at the same shared data plane.
//
// For a write end, Fork increments the shared writer count (buf.writers++) so
// that Close on the fork decrements it to the pre-fork count, and Close on the
// original decrements it further — only when both are closed does the reader
// see EOF. This mirrors Linux's get_file() reference-count increment inside
// copy_files() during fork.
//
// For a read end, Fork returns a new End pointing at the same buf without
// changing the writer count (read ends carry no reference count of their own).
//
// Panics if e is already closed: forking a closed End is a programming error
// (the caller must not inherit a file descriptor that has already been closed).
func (e *End) Fork() *End {
	if e.closed {
		panic("pipe: Fork called on a closed End")
	}
	if e.isWriter {
		e.b.writers++
		return &End{b: e.b, isWriter: true}
	}
	return &End{b: e.b}
}

// Cloexec reports whether this end was created with O_CLOEXEC. The shepherd
// copies this into the backing fdEntry.cloexec so the close-at-exec sweep
// (MAZ-113) closes it.
func (e *End) Cloexec() bool {
	return e.b.flags&OCLOEXEC != 0
}

// Nonblock reports whether this end was created with O_NONBLOCK.
func (e *End) Nonblock() bool {
	return e.b.flags&ONONBLOCK != 0
}

// Write appends p to the pipe buffer and returns the number of bytes accepted.
// It writes as many bytes as fit under the buffer cap; when the buffer is
// already full it returns (0, ErrWouldBlock) so the shepherd can apply EAGAIN
// (O_NONBLOCK) or park the writer. A zero-length write is a no-op success.
func (e *End) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	free := bufCap - len(e.b.data)
	if free <= 0 {
		return 0, ErrWouldBlock
	}
	n := len(p)
	if n > free {
		n = free
	}
	e.b.data = append(e.b.data, p[:n]...)
	return n, nil
}

// Read copies up to len(p) bytes out of the pipe buffer in FIFO order and
// returns the number copied. When the buffer is empty:
//   - if all write ends are closed, Read returns (0, nil) — EOF;
//   - otherwise Read returns (0, ErrWouldBlock) so the shepherd can park the
//     request and reply when data arrives or the last writer closes.
//
// A zero-length read returns (0, nil) without consuming the EOF/would-block
// signal, matching read(2).
func (e *End) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(e.b.data) == 0 {
		if e.b.writers == 0 {
			return 0, nil // EOF: drained and no writer can refill.
		}
		return 0, ErrWouldBlock
	}
	n := copy(p, e.b.data)
	// Drop the consumed prefix. Reslicing onto a fresh backing array keeps the
	// buffer from pinning drained bytes (the pipe is short-lived and tiny, so
	// the copy cost is negligible versus a growing, never-reclaimed slab).
	e.b.data = append([]byte(nil), e.b.data[n:]...)
	return n, nil
}

// Close closes this end. Closing a write end decrements the open-writer count;
// once it reaches zero, reads on a drained buffer return EOF. Closing a read
// end is currently a no-op (writes still succeed into the buffer cap). Close is
// idempotent on a given End: a write End decrements writers at most once.
func (e *End) Close() {
	if e.closed {
		return
	}
	e.closed = true
	if e.isWriter && e.b.writers > 0 {
		e.b.writers--
	}
}

// Park records an opaque token for a reader that found the pipe empty (and at
// least one writer still open) and is being parked by the shepherd. The token
// is returned by a later TakeWaiters once the pipe is readable or at EOF. The
// pipe package treats the token as opaque — it is the shepherd's parked Reply.
func (e *End) Park(token any) {
	e.b.waiters = append(e.b.waiters, token)
}

// TakeWaiters returns and clears the parked-reader tokens if the pipe can now
// make progress for a reader — i.e. there is buffered data OR all writers have
// closed (EOF). While the pipe is still empty with a writer open, it returns
// nil and leaves the parked tokens in place. Tokens are returned in park order
// (FIFO), so the shepherd can fulfill the oldest reader first.
func (e *End) TakeWaiters() []any {
	if len(e.b.waiters) == 0 {
		return nil
	}
	if len(e.b.data) == 0 && e.b.writers > 0 {
		return nil // still blocked: empty and a writer could still write.
	}
	w := e.b.waiters
	e.b.waiters = nil
	return w
}
