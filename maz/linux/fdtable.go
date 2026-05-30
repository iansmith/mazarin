package main

import (
	"sync"

	"mazzy/maz/linux/internal/fdtable"
	"mazzy/shared/dlist"
)

// ShepherdFilesystemData holds per-shepherd filesystem state.
// The linux shepherd maintains one of these for each caller shepherd
// that has interacted with it.
//
// The FD table itself lives in maz/linux/internal/fdtable (lifted out of
// package main so it has a host-testable home — see that package's doc and
// MAZ-122). This struct binds one shepherd's FD table to its advisory locks.
//
// mu serializes access for that one shepherd. Held by syscallHandler.handle
// for the entire dispatch so a single shepherd's syscalls remain ordered,
// while syscalls from different shepherds run concurrently on their own
// goroutines. Never held across a fsclient call's response wait — fsclient
// has its own internal mutex that already serializes the underlying IPC.
type ShepherdFilesystemData struct {
	mu    sync.Mutex
	SID   int16
	FDT   *fdtable.Table
	Locks *dlist.List[*flockEntry]
}
