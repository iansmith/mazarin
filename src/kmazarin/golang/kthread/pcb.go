package kthread

import (
	"sync"

	"kmazarin/util"
)

// PCB (Priest Control Block) represents a priest process.
// Native Mazzy syscalls return a pointer to this struct as a 64-bit token.
type PCB struct {
	// Synchronization
	Lock sync.Mutex

	// Identity - LinuxPID is for Linux emulation only (1-127)
	LinuxPID uint8
	State    PriestState

	// List membership
	listNode *util.DNode[*PCB]

	// Memory layout
	EntryPoint    uint64
	LoadBase      uint64
	HeapStart     uint64
	HeapEnd       uint64
	PageTableRoot uint64
	MappedPages   []PageMapping

	// Symbol table (populated from ELF at load time)
	Symbols map[string]uint64

	// Thin clients within this priest
	Thins           *util.DLinkedList[*ThinCB]
	NextLinuxThinID uint8

	// Kernel threads belonging to this priest
	Threads *util.DLinkedList[*KThread]

	// Async event delivery
	AsyncEventHandler    uint64
	AsyncEventTrampoline uint64

	// Event queue
	eventLock      sync.Mutex
	EventQueue     [16]AsyncEvent
	EventQueueHead uint8
	EventQueueTail uint8
	EventsInFlight uint32 // Atomic: 0=false, 1=true

	// Saved context for event delivery
	SavedELR uint64
	SavedX0  uint64
	SavedX1  uint64

	// Scheduler affinity
	AffinityCounter int32
}

// PriestState represents the state of a priest process.
type PriestState int32

const (
	PriestRunning PriestState = 1
	PriestReady   PriestState = 2
	PriestBlocked PriestState = 3
	PriestZombie  PriestState = 4
)

// PageMapping records a mapped page for cleanup.
type PageMapping struct {
	VirtAddr uint64
	PhysAddr uint64
	Size     uint64
	Flags    uint64
}

// AsyncEvent represents an asynchronous event to be delivered to a priest.
type AsyncEvent struct {
	Type uint32
	Data uint64
}

// Event types
const (
	EventThinExited   = 1
	EventPriestExited = 2
	EventSignal       = 3
	EventTimer        = 4
)
