package main

import (
	"path"

	"mazzy/shared/dlist"
)

// AT_FDCWD is the special dirfd value meaning "use the caller's CWD".
// Linux defines this as -100; *at-family syscalls accept it in lieu of a real fd.
const AT_FDCWD = -100

// AT_REMOVEDIR makes unlinkat behave like rmdir (mandatory for removing
// directories; refusing to remove non-directories).
const AT_REMOVEDIR = 0x200

// MaxFDs is the maximum number of open file descriptors.
const MaxFDs = 256

// File type constants (matching ext2 FT values used on the fs side).
const (
	ftFile uint8 = 1
	ftDir  uint8 = 2
)

// fdKind distinguishes special fds (stdin/stdout/stderr) from file fds.
type fdKind uint8

const (
	fdKindNone   fdKind = iota // slot is free
	fdKindStdin                // fd 0 — keyboard input
	fdKindStdout               // fd 1 — console output (white)
	fdKindStderr               // fd 2 — console output (red)
	fdKindFile                 // regular file (via fs IPC)
	fdKindDir                  // directory (via fs IPC)
)

// fdEntry tracks one open file descriptor. The handle field is an opaque
// fs-side handle returned by OpOpen; offset and size are managed locally.
type fdEntry struct {
	kind   fdKind
	handle uint32 // fs-side handle (from IPC OpOpen)
	offset int64  // current byte position
	size   uint32 // cached file size
	ftype  uint8  // ftFile or ftDir
	flags  int32  // O_RDONLY, O_WRONLY, O_RDWR, etc.
	path   string // absolute path that was opened (for diagnostics)
}

// fdTable manages a per-shepherd file descriptor table.
// All fields are accessed from a single goroutine (the delegate handler)
// so no locking is needed.
type fdTable struct {
	entries [MaxFDs]*fdEntry
	cwd     string // current working directory (absolute path)
}

// newFDTable creates a table with fd 0/1/2 pre-populated.
func newFDTable() *fdTable {
	t := &fdTable{cwd: "/"}
	t.entries[0] = &fdEntry{kind: fdKindStdin}
	t.entries[1] = &fdEntry{kind: fdKindStdout}
	t.entries[2] = &fdEntry{kind: fdKindStderr}
	return t
}

// ShepherdFilesystemData holds per-shepherd filesystem state.
// The linux shepherd maintains one of these for each caller shepherd
// that has interacted with it. Accessed only from the delegate handler
// goroutine (single-goroutine safety).
type ShepherdFilesystemData struct {
	SID   int16
	FDT   *fdTable
	Locks *dlist.List[*flockEntry]
}

// alloc finds the lowest free fd >= minFD and returns it, or -1 if full.
func (t *fdTable) alloc(minFD int) int {
	for i := minFD; i < MaxFDs; i++ {
		if t.entries[i] == nil {
			return i
		}
	}
	return -1
}

// get returns the entry for fd, or nil if the fd is not open.
func (t *fdTable) get(fd int) *fdEntry {
	if fd < 0 || fd >= MaxFDs {
		return nil
	}
	return t.entries[fd]
}

// put installs an entry at the given fd slot.
func (t *fdTable) put(fd int, e *fdEntry) {
	if fd >= 0 && fd < MaxFDs {
		t.entries[fd] = e
	}
}

// free closes and removes an fd entry.
func (t *fdTable) free(fd int) {
	if fd >= 0 && fd < MaxFDs {
		t.entries[fd] = nil
	}
}

// resolvePath converts a possibly-relative path to absolute using the CWD,
// then canonicalizes (collapses `//`, eliminates `.` and `..`). Paths
// starting with '/' are normalized as-is. Empty paths return an error-
// signaling empty string.
//
// This is the legacy entry point used by handlers that don't take a dirfd
// (chdir on a path string). New *at-family handlers should use resolveAt.
func (t *fdTable) resolvePath(p string) string {
	if len(p) == 0 {
		return ""
	}
	if p[0] != '/' {
		if t.cwd == "/" {
			p = "/" + p
		} else {
			p = t.cwd + "/" + p
		}
	}
	return path.Clean(p)
}

// resolveAt resolves a (dirfd, path) pair to a canonical absolute path,
// applying Linux *at-family rules:
//   - empty path → EINVAL (callers that want AT_EMPTY_PATH must check it
//     before calling resolveAt; we don't implement AT_EMPTY_PATH here)
//   - absolute path → ignore dirfd, just normalize
//   - dirfd == AT_FDCWD → resolve relative to the caller's CWD
//   - dirfd >= 0 → look up the FD; must be an open directory; resolve
//     relative to that directory's stored absolute path
//
// On error returns ("", -errno).
func (t *fdTable) resolveAt(dirfd int32, p string) (string, int64) {
	if len(p) == 0 {
		return "", -22 // EINVAL
	}
	if p[0] == '/' {
		return path.Clean(p), 0
	}
	if dirfd == AT_FDCWD {
		return t.resolvePath(p), 0
	}
	e := t.get(int(dirfd))
	if e == nil || e.kind == fdKindNone {
		return "", -9 // EBADF
	}
	if e.kind != fdKindDir {
		return "", -20 // ENOTDIR
	}
	base := e.path
	if base == "" {
		base = "/"
	}
	if base == "/" {
		return path.Clean("/" + p), 0
	}
	return path.Clean(base + "/" + p), 0
}
