package kthread

import (
	"sync"

	"kmazarin/util"
)

// TID is a thread identifier (64-bit for native, but stored internally).
type TID int64

// KThread represents a kernel-scheduled thread.
// Native Mazzy syscalls return a pointer to this struct as a 64-bit token.
type KThread struct {
	// Identity
	TID    TID
	Priest *PCB // Backpointer for page table on context switch
	State  KThreadState

	// List memberships
	priestNode *util.DNode[*KThread]
	schedNode  *util.DNode[*KThread]

	// Saved CPU context (when not running)
	// Reuses ThreadContext from thread.go
	Context ThreadContext

	// Stack for this thread
	StackBase uint64
	StackTop  uint64

	// CPU affinity
	CurrentCPU   int32
	AffinityMask uint64

	// Scheduling
	Priority       int32
	TimeSlice      int32
	IsKernelThread bool

	// Lock for thread-specific state
	Lock sync.Mutex
}

// KThreadState represents the state of a kernel thread.
type KThreadState int32

const (
	KThreadRunning KThreadState = 1
	KThreadReady   KThreadState = 2
	KThreadBlocked KThreadState = 3
	KThreadZombie  KThreadState = 4
)

// Scheduling constants
const (
	SignalTicksLeft      int32 = -130 // Thread is blocked
	TickPeriodMs               = 20   // Timer fires every 20ms
	ThreadTimeSliceTicks int32 = 5    // 100ms = 5 ticks
	PriestAffinityTicks  int32 = 15   // 300ms = 15 ticks
)
