package main

import (
	"mazzy/shared/dlist"
)

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

// resolvePath converts a possibly-relative path to absolute using the CWD.
// Paths starting with '/' are returned as-is. Empty paths return an error-
// signaling empty string.
func (t *fdTable) resolvePath(path string) string {
	if len(path) == 0 {
		return ""
	}
	if path[0] == '/' {
		return path
	}
	// Relative path — prepend CWD.
	if t.cwd == "/" {
		return "/" + path
	}
	return t.cwd + "/" + path
}
