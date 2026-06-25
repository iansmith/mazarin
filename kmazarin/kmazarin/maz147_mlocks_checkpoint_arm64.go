//go:build arm64

package main

// MAZ-147 Option B m.locks checkpoint — amd64-only for now (validation is
// amd64-first; the ARM64 save+restore pair lands together when ARM64 validation
// begins — see maz147_mlocks_checkpoint_amd64.go and design doc §3/§8). The
// shared doContextSwitchImpl save call site compiles against this no-op so the
// scheduler path stays arch-portable. ARM64 g0.m.locks is left UNTOUCHED: a
// half-fix that zeroed it here without an ARM64 asm restore (in the ARM64
// load-context/eret chokepoint) would lose the count and break the kernel.
//
//go:nosplit
func mlockCheckpointSave(outgoing *Thread) {}

// g0PreemptHoldsMLocks: amd64-only (the timer-preempt m.locks SKIP — design §8c/§8d);
// no-op false on arm64 so the shared checkThreadPreemptionImpl guard compiles and
// never skips. ARM64's save/restore/skip land together in the deferred arm64 phase.
//
//go:nosplit
func g0PreemptHoldsMLocks(framePtr uintptr) bool { return false }
