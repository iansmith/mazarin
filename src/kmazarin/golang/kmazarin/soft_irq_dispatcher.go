//go:build arm64

package main

import (
	"kmazarin/console"
	"sync"
	"unsafe"
)

// ============================================================================
// Go-Level Soft IRQ Dispatcher
// ============================================================================
//
// This file implements the user-facing soft IRQ subscription and dispatch system.
// Device drivers subscribe to IRQs with channels, and the dispatcher goroutine
// delivers bundles to the appropriate subscribers.
//
// Usage:
//   irqChan := make(chan SoftIRQBundle, 4)
//   unsub := SubscribeSoftIRQ(123, irqChan)
//   defer unsub()
//   for bundle := range irqChan {
//       // Handle IRQ
//   }

// softIRQSubscribers maps IRQ numbers to subscriber channels.
// Protected by softIRQSubLock.
var softIRQSubscribers map[uint32]chan SoftIRQBundle

// softIRQSubLock protects the subscribers map.
// This is a separate lock from schedulerLock to avoid nested locking.
var softIRQSubLock sync.Mutex

// InitSoftIRQDispatcher initializes the Go-level dispatcher.
// Called during kernel startup.
func InitSoftIRQDispatcher() {
	softIRQSubscribers = make(map[uint32]chan SoftIRQBundle)
}

// SubscribeSoftIRQ registers a channel to receive IRQs for the given number.
// Only one subscriber per IRQ number is allowed.
// Returns an unsubscribe function.
//
// Example:
//   ch := make(chan SoftIRQBundle, 4)
//   unsub := SubscribeSoftIRQ(123, ch)
//   defer unsub()
func SubscribeSoftIRQ(irqNum uint32, ch chan SoftIRQBundle) func() {
	softIRQSubLock.Lock()
	softIRQSubscribers[irqNum] = ch
	softIRQSubLock.Unlock()

	return func() {
		softIRQSubLock.Lock()
		delete(softIRQSubscribers, irqNum)
		softIRQSubLock.Unlock()
	}
}

// SoftIRQDispatcher is the main dispatcher goroutine.
// It waits for soft IRQs and dispatches to subscribers.
//
// This goroutine must be started during kernel initialization.
func SoftIRQDispatcher() {
	console.KPrintln("[SoftIRQ] Dispatcher starting")

	var bundle SoftIRQBundle

	// Register as the soft IRQ dispatcher
	result := RegisterSoftIRQDispatcher()
	if result < 0 {
		console.KPrintf("[SoftIRQ] Failed to register dispatcher: %d\n", result)
		return
	}

	for {
		// First drain overflow queue
		for GetPendingSoftIRQ(uint64(uintptr(unsafe.Pointer(&bundle)))) {
			dispatchBundle(bundle)
		}

		// Block waiting for next soft IRQ
		nextThread := ThreadBlockSoftIRQ(&NormalSchedulerFunc, uint64(uintptr(unsafe.Pointer(&bundle))))
		if nextThread == 0 {
			// No thread to switch to, try again after yielding
			continue
		}

		// We were woken - bundle is filled by softIRQWakeDispatcher
		dispatchBundle(bundle)
	}
}

// dispatchBundle dispatches a soft IRQ bundle to the registered subscriber.
func dispatchBundle(bundle SoftIRQBundle) {
	softIRQSubLock.Lock()
	ch, ok := softIRQSubscribers[bundle.IRQNum]
	softIRQSubLock.Unlock()

	if ok {
		// Non-blocking send to subscriber
		select {
		case ch <- bundle:
			// Delivered
		default:
			// Subscriber channel full - drop
			console.KPrintf("[SoftIRQ] Dropped IRQ %d (subscriber busy)\n", bundle.IRQNum)
		}
	}
	// IRQ with no subscriber is silently dropped
}
