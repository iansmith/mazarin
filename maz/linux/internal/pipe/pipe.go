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
//
// MAZ-121 Phase 0: this is a STUB. The four behaviors below are specified by
// the red tests in pipe_test.go and fail against this stub. The implementation
// phase makes them pass.

package pipe

import "errors"

// O_CLOEXEC / O_NONBLOCK as seen on the pipe2 flags argument. Linux ARM64 and
// x86_64 share these values (asm-generic + x86 agree).
const (
	OCLOEXEC  = 0x80000
	ONONBLOCK = 0x800
)

// ErrWouldBlock is returned by Read when the buffer is empty but write ends
// remain open, and by Write when a non-blocking write hits a full buffer.
// The shepherd translates this into either EAGAIN (O_NONBLOCK) or a parked
// request (blocking), per the deferred-Reply model.
var ErrWouldBlock = errors.New("pipe: operation would block")

// End is one end of a pipe — either the read end or the write end. Both ends
// share the same underlying buffer. The Cloexec / Nonblock flags are derived
// from the pipe2 flags argument and applied to both ends at creation.
type End struct {
	// STUB — fields defined by the implementation phase.
}

// errStub is returned by every stub method below so the Phase 0 red tests
// fail with their own assertion messages (rather than one panic aborting the
// whole test binary). The implementation phase replaces these stubs.
var errStub = errors.New("pipe: not implemented (MAZ-121 Phase 0 stub)")

// Cloexec reports whether this end was created with O_CLOEXEC. The shepherd
// copies this into the backing fdEntry.cloexec so the close-at-exec sweep
// (MAZ-113) closes it.
func (e *End) Cloexec() bool {
	return false // STUB
}

// Nonblock reports whether this end was created with O_NONBLOCK.
func (e *End) Nonblock() bool {
	return false // STUB
}

// Write appends p to the pipe buffer and returns the number of bytes accepted.
// On a non-blocking write to a full buffer it returns (0, ErrWouldBlock).
func (e *End) Write(p []byte) (int, error) {
	return 0, errStub // STUB
}

// Read copies up to len(p) bytes out of the pipe buffer in FIFO order and
// returns the number copied. When the buffer is empty:
//   - if all write ends are closed, Read returns (0, nil) — EOF;
//   - otherwise Read returns (0, ErrWouldBlock) so the shepherd can park the
//     request and reply when data arrives or the last writer closes.
func (e *End) Read(p []byte) (int, error) {
	return 0, errStub // STUB
}

// Close closes this end. Closing the last write end makes subsequent reads on
// a drained buffer return EOF. Idempotent.
func (e *End) Close() {
	// STUB — no-op.
}

// New creates a linked (read, write) End pair sharing a fresh buffer. flags is
// the pipe2(2) flags argument (O_CLOEXEC / O_NONBLOCK); both ends inherit the
// derived Cloexec / Nonblock flags.
func New(flags int) (read *End, write *End) {
	// STUB — returns non-nil ends so tests reach their assertions instead of
	// nil-dereferencing. Both ends are empty; all methods report stub values.
	return &End{}, &End{}
}
