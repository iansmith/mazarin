//go:build arm64 || amd64

package main

import "mazzy/kmazarin/ds"

// scheduler.go - Testable Scheduler Framework
//
// This file defines a dependency injection framework for testing scheduler behavior
// without requiring real hardware, interrupts, or timing.
//
// ## Design Goals
//
// 1. **Production Simplicity**: Real exception/interrupt handlers use a single
//    "normal" SchedulerFunc that points to real hardware functions. No overhead,
//    no indirection costs in production.
//
// 2. **Test Controllability**: Tests can provide custom SchedulerFunc implementations
//    to simulate complex scenarios:
//    - Control exact timing via fake CurrentTime
//    - Simulate multi-core race conditions (multiple events "at same time")
//    - Verify lock discipline via fake Enable/Disable functions
//    - Create edge cases (thread exhausts timeslice exactly at interrupt, etc.)
//
// 3. **Lock Discipline Verification**: Tests can inject fake Enable/DisableDAIF
//    functions that track whether interrupts are properly disabled during critical
//    sections. Any violation can be caught and reported.
//
// 4. **Deterministic Testing**: By controlling the clock (CurrentTime), we can
//    create reproducible test scenarios with precise timing windows.
//
// ## Architecture
//
// **SchedulerFunc**: A collection of function pointers for all primitives the
// scheduler needs (time, locks, interrupt control). Production uses real functions,
// tests use fakes.
//
// **Scheduler**: A stateless type with methods like HandleInterrupt(), TimerTick(),
// KillCurrentThread(). All state lives in global variables (threadList, queues, etc.).
// Methods take a *SchedulerFunc to perform operations.
//
// ## Usage
//
// Production code:
//   scheduler.HandleInterrupt(&normalSchedulerFunc, TIMER_INTERRUPT)
//
// Test code:
//   testFunc := &SchedulerFunc{
//       CurrentTime: func(fakeTime uint64) uint64 { return 1000000 },
//       // ... other fakes ...
//   }
//   scheduler.HandleInterrupt(testFunc, TIMER_INTERRUPT)
//   // Verify expected state changes
//
// ## Notes
//
// - Exception handlers (Data Abort, etc.) automatically disable interrupts in
//   hardware, so Enable/DisableDAIF may not be needed for those paths
// - Timer interrupts and software interrupts (SVC) require explicit interrupt
//   management
// - Tests should verify that interrupt state is properly restored after operations

// SchedulerFunc holds function pointers for all primitives the scheduler needs.
// Production uses real implementations, tests use fakes for controllability.
type SchedulerFunc struct {
	// CurrentTime returns the current counter value.
	// If fakeTime != 0, returns fakeTime (for testing precise scenarios).
	// If fakeTime == 0, reads hardware counter.
	CurrentTime func(fakeTime uint64) uint64

	// DisableAndSaveDAIF disables interrupts and returns saved state.
	// Tests can use this to verify lock discipline.
	// Returns saved DAIF state to be passed to EnableAndRestoreDAIF.
	DisableAndSaveDAIF func() uint64

	// EnableAndRestoreDAIF restores interrupt state.
	// Tests can verify this is called correctly (balanced with Disable).
	EnableAndRestoreDAIF func(savedState uint64)

	// StateCheck is called at various points to verify scheduler invariants.
	// If nil, no checks are performed (production case).
	// Tests can inject checks to catch invariant violations.
	// Example: verify ready queue contains no running threads.
	StateCheck func(checkpoint string)
}

// NormalSchedulerFunc is the production SchedulerFunc using real hardware functions.
// All exception handlers and interrupt handlers should use this.
var NormalSchedulerFunc = SchedulerFunc{
	CurrentTime:          ds.CurrentTime,
	DisableAndSaveDAIF:   SaveAndDisableIRQs,
	EnableAndRestoreDAIF: RestoreIRQs,
	StateCheck:           nil, // No checks in production
}

// Scheduler is a stateless type providing scheduler operations.
// All state lives in global variables (threadList, readyQueue, etc.).
// Methods take a *SchedulerFunc to allow testing with fake implementations.
type Scheduler struct{}

// Global scheduler instance
var scheduler Scheduler

// InterruptType identifies the type of interrupt being handled
type InterruptType uint64

const (
	InterruptTimer InterruptType = iota
	InterruptSyscall
	InterruptDataAbort
	InterruptUnaligned
	// Add more as needed
)

// ExceptionType identifies the type of exception that requires thread termination
type ExceptionType uint64

const (
	ExceptionDataAbort ExceptionType = iota
	ExceptionUnalignedInstruction
	ExceptionUnalignedStack
	ExceptionIllegalInstruction
	// Add more as needed
)
