// Package fdtable holds the linux shepherd's per-process file descriptor
// table: the Entry/Table types, the *at-family path resolution, and the
// O_CLOEXEC contract that fork/exec depends on.
//
// Why this lives in internal/: maz/linux's main package is `package main`
// (a shepherd binary), which makes unit-testing painful — `task test` runs
// `./maz/linux/internal/...` only. Lifting the FD table here keeps the linux
// shepherd thin and gives the contract a host-testable home. Matches the
// pattern at maz/linux/internal/processrecord and maz/protocol-http/internal.
//
// # The O_CLOEXEC contract (MAZ-122)
//
// Each Entry carries a Cloexec bit. Three sites cooperate to give it the
// Linux close-on-exec semantics, all reading/writing the SAME field through
// the helpers here so the behavior is consistent everywhere:
//
//	1. SET   — openat (this ticket) and pipe2 (MAZ-121) set Cloexec via
//	   CloexecFromFlags(flags) when the caller passes O_CLOEXEC.
//	2. TOGGLE — fcntl(F_SETFD, FD_CLOEXEC) / F_GETFD flips/reads it (MAZ-119).
//	3. CLOSE — the exec step sweeps every Cloexec FD via CloseCloexecFDs and
//	   closes it, so it does not leak into the exec'd child (MAZ-113). This is
//	   the linchpin of the vfork errpipe handshake: the errpipe write-end is
//	   opened O_CLOEXEC, so a successful exec closes it and the parent's read
//	   gets EOF = "exec succeeded".
//
// Concurrency: NOT goroutine-safe on its own. In the linux shepherd a Table
// is owned by one ShepherdFilesystemData and accessed only while that
// shepherd's mutex is held, so a single shepherd's syscalls stay ordered.
package fdtable

import "path"

// AT_FDCWD is the special dirfd value meaning "use the caller's CWD".
// Linux defines this as -100; *at-family syscalls accept it in lieu of a real fd.
const AT_FDCWD = -100

// AT_REMOVEDIR makes unlinkat behave like rmdir (mandatory for removing
// directories; refusing to remove non-directories).
const AT_REMOVEDIR = 0x200

// MaxFDs is the maximum number of open file descriptors.
const MaxFDs = 256

// OCLOEXEC is the Linux O_CLOEXEC flag value (octal 0o2000000 == 0x80000).
// Identical on ARM64 and amd64. This is the single source of truth — the
// openat/pipe2/fcntl paths all derive the cloexec bit from it via
// CloexecFromFlags so the value is never duplicated.
const OCLOEXEC = 0x80000

// File type constants (matching ext2 FT values used on the fs side).
const (
	FtFile uint8 = 1
	FtDir  uint8 = 2
)

// Kind distinguishes special fds (stdin/stdout/stderr) from file fds.
type Kind uint8

const (
	KindNone   Kind = iota // slot is free
	KindStdin              // fd 0 — keyboard input
	KindStdout             // fd 1 — console output (white)
	KindStderr             // fd 2 — console output (red)
	KindFile               // regular file (via fs IPC)
	KindDir                // directory (via fs IPC)
)

// CloexecFromFlags reports whether the given open flags request close-on-exec.
// It is the single derivation point openat, pipe2, and fcntl all call so the
// cloexec bit is computed identically everywhere.
func CloexecFromFlags(flags int32) bool {
	return uint32(flags)&OCLOEXEC != 0
}

// Entry tracks one open file descriptor. The Handle field is an opaque
// fs-side handle returned by OpOpen; Offset and Size are managed locally.
type Entry struct {
	Kind   Kind
	Handle uint32 // fs-side handle (from IPC OpOpen)
	Inum   uint32 // ext2 inode number — stable identity for cache keys
	Offset int64  // current byte position
	Size   uint32 // cached file size
	Ftype  uint8  // FtFile or FtDir
	Flags  int32  // O_RDONLY, O_WRONLY, O_RDWR, etc.
	Path   string // absolute path that was opened (for diagnostics)

	// Cloexec marks this fd close-on-exec. Set at creation (openat/pipe2 via
	// CloexecFromFlags), toggled by fcntl(F_SETFD), and acted on by the exec
	// step (CloseCloexecFDs). See the package doc for the full contract.
	Cloexec bool

	// WriteBuf accumulates sequential write() data in the linux shepherd so
	// many small writes from bleve/zapx collapse into one IPC round-trip on
	// fsync/close. WriteBufOff is the file offset where the buffer starts.
	// nil means buffering is disabled for this fd.
	WriteBuf    []byte
	WriteBufOff int64
}

// Table manages a per-shepherd file descriptor table.
// All fields are accessed from a single goroutine (the delegate handler)
// so no locking is needed.
type Table struct {
	entries [MaxFDs]*Entry
	Cwd     string // current working directory (absolute path)
}

// New creates a table with fd 0/1/2 pre-populated.
func New() *Table {
	t := &Table{Cwd: "/"}
	t.entries[0] = &Entry{Kind: KindStdin}
	t.entries[1] = &Entry{Kind: KindStdout}
	t.entries[2] = &Entry{Kind: KindStderr}
	return t
}

// Alloc finds the lowest free fd >= minFD and returns it, or -1 if full.
func (t *Table) Alloc(minFD int) int {
	for i := minFD; i < MaxFDs; i++ {
		if t.entries[i] == nil {
			return i
		}
	}
	return -1
}

// Get returns the entry for fd, or nil if the fd is not open.
func (t *Table) Get(fd int) *Entry {
	if fd < 0 || fd >= MaxFDs {
		return nil
	}
	return t.entries[fd]
}

// Put installs an entry at the given fd slot.
func (t *Table) Put(fd int, e *Entry) {
	if fd >= 0 && fd < MaxFDs {
		t.entries[fd] = e
	}
}

// Free closes and removes an fd entry.
func (t *Table) Free(fd int) {
	if fd >= 0 && fd < MaxFDs {
		t.entries[fd] = nil
	}
}

// Each calls fn for every open fd, in ascending fd order, passing the slot's
// index (the fd number) alongside its entry. Iteration stops early if fn
// returns false — mirroring proc.ShepherdStorage.ForEach. This lets callers
// walk the live table without exposing the backing array.
func (t *Table) Each(fn func(fd int, e *Entry) bool) {
	for fd, e := range t.entries {
		if e != nil && !fn(fd, e) {
			return
		}
	}
}

// CloseCloexecFDs implements the exec-step sweep: it closes (and frees)
// exactly the FDs whose Cloexec bit is set, leaving every non-cloexec FD —
// including the stdio FDs 0/1/2 — intact.
//
// closeHandle is invoked once per swept FD that owns an fs handle
// (Handle != 0), so the caller can release the fs-side handle through its own
// fs client (MAZ-113 passes h.fs.Close). FDs with Handle == 0 (e.g. pipe ends
// or stdio) are freed without a callback. The callback decouples the sweep
// from the fs IPC client so it is host-testable.
//
// Unlike sysClose, this does not preserve orphaned handles for surviving
// mmaps: exec replaces the address space, so there are no surviving mappings
// to flush.
func (t *Table) CloseCloexecFDs(closeHandle func(handle uint32)) {
	for fd, e := range t.entries {
		if e == nil || !e.Cloexec {
			continue
		}
		if e.Handle != 0 && closeHandle != nil {
			closeHandle(e.Handle)
		}
		t.Free(fd)
	}
}

// Dup3 implements dup3(oldfd, newfd, flags): it makes newfd refer to the same
// open file as oldfd, first closing whatever entry occupied newfd. On success
// it returns (newfd, 0); on error it returns (-1, -errno) and leaves the table
// unchanged. Linux dup3 semantics:
//
//   - oldfd must be open               → else EBADF
//   - newfd must be in range [0,MaxFDs) → else EBADF
//   - oldfd == newfd                    → EINVAL (the defining difference from
//     dup2, which would no-op and return oldfd)
//   - flags may carry O_CLOEXEC, setting the new fd's Cloexec bit.
//
// The new entry is an independent struct COPY of the old one — it shares the
// open-file identity (Handle/Inum/Path) but gets its own Offset. (Real Linux
// shares one open-file description; this shepherd keeps the offset on the entry,
// so a copy is the closest approximation and is sufficient for the fork/exec
// inheritance use case.) The copy never inherits oldfd's WriteBuf: a half-built
// write buffer belongs to one fd, not its dup.
//
// closeNewFD, if non-nil, is invoked with the entry that previously occupied
// newfd (still installed at that slot when the callback runs), so the caller
// can flush/release that entry's fs resources through its own fs client before
// it is overwritten. It is called for every displaced entry; the callback
// itself decides what disposition each entry needs.
func (t *Table) Dup3(oldfd, newfd int, flags int32, closeNewFD func(e *Entry)) (int, int64) {
	old := t.Get(oldfd)
	if old == nil {
		return -1, -9 // EBADF
	}
	if newfd < 0 || newfd >= MaxFDs {
		return -1, -9 // EBADF
	}
	if oldfd == newfd {
		return -1, -22 // EINVAL
	}
	if prev := t.Get(newfd); prev != nil {
		if closeNewFD != nil {
			closeNewFD(prev)
		}
		t.Free(newfd)
	}
	dup := *old
	dup.WriteBuf = nil
	dup.WriteBufOff = 0
	dup.Cloexec = CloexecFromFlags(flags)
	t.Put(newfd, &dup)
	return newfd, 0
}

// ResolvePath converts a possibly-relative path to absolute using the CWD,
// then canonicalizes (collapses `//`, eliminates `.` and `..`). Paths
// starting with '/' are normalized as-is. Empty paths return an error-
// signaling empty string.
//
// This is the legacy entry point used by handlers that don't take a dirfd
// (chdir on a path string). New *at-family handlers should use ResolveAt.
func (t *Table) ResolvePath(p string) string {
	if len(p) == 0 {
		return ""
	}
	if p[0] != '/' {
		if t.Cwd == "/" {
			p = "/" + p
		} else {
			p = t.Cwd + "/" + p
		}
	}
	return path.Clean(p)
}

// ResolveAt resolves a (dirfd, path) pair to a canonical absolute path,
// applying Linux *at-family rules:
//   - empty path → EINVAL (callers that want AT_EMPTY_PATH must check it
//     before calling ResolveAt; we don't implement AT_EMPTY_PATH here)
//   - absolute path → ignore dirfd, just normalize
//   - dirfd == AT_FDCWD → resolve relative to the caller's CWD
//   - dirfd >= 0 → look up the FD; must be an open directory; resolve
//     relative to that directory's stored absolute path
//
// On error returns ("", -errno).
func (t *Table) ResolveAt(dirfd int32, p string) (string, int64) {
	if len(p) == 0 {
		return "", -22 // EINVAL
	}
	if p[0] == '/' {
		return path.Clean(p), 0
	}
	if dirfd == AT_FDCWD {
		return t.ResolvePath(p), 0
	}
	e := t.Get(int(dirfd))
	if e == nil || e.Kind == KindNone {
		return "", -9 // EBADF
	}
	if e.Kind != KindDir {
		return "", -20 // ENOTDIR
	}
	base := e.Path
	if base == "" {
		base = "/"
	}
	if base == "/" {
		return path.Clean("/" + p), 0
	}
	return path.Clean(base + "/" + p), 0
}
