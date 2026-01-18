package kthread

import "kmazarin/util"

// ThinCB (Thin Control Block) represents a thin client program.
// Native Mazzy syscalls return a pointer to this struct as a 64-bit token.
type ThinCB struct {
	// Identity - LinuxThinID is for Linux emulation only (1-255)
	LinuxThinID uint8
	Priest      *PCB
	State       ThinState

	// List membership
	listNode *util.DNode[*ThinCB]

	// Memory layout
	EntryPoint uint64
	LoadBase   uint64
	StackBase  uint64
	StackTop   uint64

	// Mapped pages (for cleanup)
	MappedPages []PageMapping

	// Exit status
	ExitCode int32
}

// ThinState represents the state of a thin client.
type ThinState int32

const (
	ThinRunning ThinState = 1
	ThinReady   ThinState = 2
	ThinZombie  ThinState = 3
)
