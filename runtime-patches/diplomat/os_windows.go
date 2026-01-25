// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// DIPLOMAT OVERLAY: Stub out Windows OS dependencies for UEFI environment
//
// This file replaces runtime/os_windows.go with UEFI-compatible stubs.
// For Phase 0, we provide minimal no-op implementations to eliminate
// kernel32.dll dependencies.

package runtime

import (
	"internal/abi"
	"unsafe"
)

type mOS struct{}
type sigset struct{}

//go:linkname os_sigpipe os.sigpipe
func os_sigpipe() {
	throw("too many writes on closed pipe")
}

func sigpanic() {
	gp := getg()
	if !canpanic() {
		throw("unexpected signal during runtime execution")
	}

	panicCheck1(getcallerpc(), getcallersp())
	panicCheck2(getcallerpc())
	sp := getcallersp()
	pc := getcallerpc()

	// Invoke a signal panic
	sigpanic0(gp, pc, sp)
}

//go:nosplit
func exit(code int32) {
	// For UEFI, we can't really exit
	// Just hang forever
	for {
	}
}

//go:nosplit
func exitThread(wait *atomic.Uint32) {
	// For Phase 0, no threading
	// Hang forever
	for {
	}
}

var  urandom_dev = []byte("/dev/urandom\x00")

//go:nosplit
func readRandom(r []byte) int {
	// For Phase 0, return zeros
	// TODO: Use UEFI RNG protocol in Phase 1
	for i := range r {
		r[i] = 0
	}
	return len(r)
}

func goenvs() {
	// No environment variables in UEFI Phase 0
}

func initsig(preinit bool) {
	// No signal handling in UEFI
}

// May run with m.p==nil, so write barriers are not allowed.
//
//go:nowritebarrier
func newosproc(mp *m) {
	// For Phase 0, no threading support
	// Just throw if someone tries to create a thread
	throw("newosproc not implemented in diplomat Phase 0")
}

// Called to do synchronous initialization of Go code built with
// -buildmode=c-archive or -buildmode=c-shared.
// None of the Go runtime is initialized.
//
//go:nosplit
//go:nowritebarrierrec
func libpreinit() {
	initsig(true)
}

func osinit() {
	// Basic OS initialization
	// For UEFI, we don't have much to do here
	ncpu = 1 // Single CPU for Phase 0
	physPageSize = 4096
	initBloc()
}

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() ([]string, []string) {
	// No command-line args in UEFI Phase 0
	return nil, nil
}

func raise(sig uint32) {
	// No signals in UEFI
	throw("raise: no signals in UEFI")
}

func signalM(mp *m, sig int) {
	// No signals
}

// sigreturn is only used on Darwin/amd64. Not needed for Windows/x64.
func sigreturn()

func makeGContext(mp *m, gp *g) {
	// For Phase 0, we don't need complex context setup
}

//go:nosplit
func osyield() {
	// For Phase 0, do nothing
	// TODO: Could call UEFI Stall() or similar
}

//go:nosplit
func osyield_no_g() {
	osyield()
}

const _SIGSEGV = 0xc0000005

//go:nosplit
func usleep_no_g(us uint32) {
	// For Phase 0, busy wait
	// This is terrible but works for testing
	// TODO: Use UEFI timer services in Phase 1
	start := nanotime()
	end := start + int64(us)*1000
	for nanotime() < end {
		// busy wait
	}
}

//go:nosplit
func usleep(us uint32) {
	usleep_no_g(us)
}

// Memory management functions are provided by mem_windows.go overlay

//go:nosplit
func walltime() (sec int64, nsec int32) {
	// For Phase 0, return a fixed time
	// TODO: Use UEFI GetTime() in Phase 1
	return 0, 0
}

//go:nosplit
func nanotime1() int64 {
	// For Phase 0, return a monotonic counter
	// TODO: Use UEFI timer in Phase 1
	// For now, just return 0
	return 0
}

func sigmask() {
	// No signals in UEFI
}

func setProcessCPUProfiler(hz int32) {
	// No profiling in Phase 0
}

func setThreadCPUProfiler(hz int32) {
	// No profiling in Phase 0
}

func sigdisable(uint32) {
	// No signals
}

func sigenable(uint32) {
	// No signals
}

func sigignore(uint32) {
	// No signals
}

// Write to standard error (for debugging)
//
//go:nosplit
func write1(fd uintptr, p unsafe.Pointer, n int32) int32 {
	// For Phase 0, redirect all writes to UEFI console
	// We'll implement this later when we have ConOut access
	// For now, just pretend we wrote everything
	return n
}

//go:nosplit
func usleep_no_g_for_asyncpreempt(ns int64) {
	// Phase 0 - no preemption
}

// initHeap initializes heap memory from UEFI
// This will be called by mallocinit
func initHeap() {
	// Phase 0: No heap initialization yet
	// The runtime will use a fixed arena
}
