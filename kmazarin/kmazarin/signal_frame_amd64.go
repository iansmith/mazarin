//go:build amd64

package main

// BuildSignalFrame is a stub for amd64 — signal delivery is not yet
// implemented on this architecture.  DeliverPendingSignal is never
// reached in practice because PendingSignals is always 0.
//
//go:nosplit
func BuildSignalFrame(_ *Thread, _ int, _ *SignalAction) {}

// RestoreFromSignalFrame is a stub for amd64.
//
//go:nosplit
func RestoreFromSignalFrame(_ *Thread) {}
